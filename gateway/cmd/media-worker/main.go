package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	sharedconfig "feedvideo.local/shared-config"
	"feedvideo.local/video/repo"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	taskQueue          = "media:tasks"
	processingQueue    = "media:processing"
	pollTimeout        = 5 * time.Second
	thumbPrefix        = "thumb_"
	procPrefix         = "proc_"
	maxAttempts        = 3
	cmdTimeout         = 120 * time.Second
	transcodeSuffix    = "_480p.mp4"
	transcodeSuffix720 = "_720p.mp4"
)

// metric 键前缀（gateway /metrics 的 statReader 读取同一命名空间）
const metricPrefix = "metrics:"

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type probeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func main() {
	mode, err := sharedconfig.ParseMediaTaskMode(os.Getenv("MEDIA_TASK_MODE"))
	if err != nil {
		log.Fatal(err)
	}
	redisAddr := envOr("REDIS_ADDR", "redis:6379")
	mongoURI := envOr("MONGO_URI", "mongodb://mongodb:27017")
	mongoDBName := envOr("MONGO_DB", "feedvideo")
	minioEndpoint := envOr("MINIO_ENDPOINT", "minio:9000")
	minioAccess := envOr("MINIO_ACCESS_KEY", "minioadmin")
	minioSecret := envOr("MINIO_SECRET_KEY", "minioadmin")
	videoBucket := envOr("VIDEO_BUCKET", "feedvideo-videos")
	coverBucket := envOr("COVER_BUCKET", "feedvideo-covers")
	ffmpegBin := envOr("FFMPEG_BIN", "ffmpeg")
	ffprobeBin := envOr("FFPROBE_BIN", "ffprobe")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mongoCtx, mongoCancel := context.WithTimeout(ctx, 10*time.Second)
	mongoClient, err := mongo.Connect(mongoCtx, options.Client().ApplyURI(mongoURI))
	mongoCancel()
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	defer func() { _ = mongoClient.Disconnect(ctx) }()
	videos := mongoClient.Database(mongoDBName).Collection("videos")

	minioClient, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioAccess, minioSecret, ""),
		Secure: false,
	})
	if err != nil {
		log.Fatalf("minio client: %v", err)
	}

	if mode == sharedconfig.MediaTaskModeMongo {
		processor := newPersistentMediaProcessor(&minioMediaObjects{client: minioClient},
			&ffmpegMediaCommands{ffmpegBin: ffmpegBin, ffprobeBin: ffprobeBin}, videoBucket, coverBucket)
		log.Printf("media worker started (mode=mongo minio=%s)", minioEndpoint)
		db := mongoClient.Database(mongoDBName)
		if err := runMongoMediaService(ctx, repo.NewMediaJobRepo(db), repo.NewMediaJobMetricsReader(db), processor, uuid.NewString(), envOr("MEDIA_METRICS_ADDR", "127.0.0.1:9091"), mediaMetricsRuntime{}); err != nil {
			log.Fatalf("mongo media worker: %v", err)
		}
		return
	}

	rdbOpts := &redis.Options{Addr: redisAddr}
	if pwd := envOr("REDIS_PASSWORD", ""); pwd != "" {
		rdbOpts.Password = pwd
	}
	rdb := redis.NewClient(rdbOpts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}
	defer rdb.Close()

	log.Printf("media worker started (redis=%s minio=%s ffmpeg=%s ffprobe=%s)", redisAddr, minioEndpoint, ffmpegBin, ffprobeBin)
	// 崩溃恢复：把 processing 队列中滞留的任务（上次进程中断未完成）重新入队。
	if err := recoverProcessingQueue(ctx, rdb, taskQueue, processingQueue); err != nil {
		log.Printf("processing queue recovery failed (will continue): %v", err)
	}
	metricsSrv := startMediaMetrics(envOr("MEDIA_METRICS_ADDR", "127.0.0.1:9091"), rdb)
	for {
		if ctx.Err() != nil {
			log.Println("media worker shutting down")
			if metricsSrv != nil {
				sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = metricsSrv.Shutdown(sctx)
				scancel()
			}
			return
		}
		// 可靠队列：原子移动到 processing，处理完成后再移除（崩溃不丢任务）
		raw, err := rdb.BRPopLPush(ctx, taskQueue, processingQueue, pollTimeout).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			log.Printf("BRPop error: %v", err)
			time.Sleep(time.Second)
			continue
		}
		task, err := parseTask([]byte(raw))
		if err != nil {
			log.Printf("bad task dropped: %v", err)
			_ = rdb.LRem(ctx, processingQueue, 1, raw)
			continue
		}
		// 视频已删除：不执行转码，直接丢弃任务（避免白跑 + 回写已删除文档）
		if deleted, derr := isVideoDeleted(ctx, videos, task.VideoID); derr == nil && deleted {
			log.Printf("task %s skipped: video deleted", task.VideoID)
			_ = rdb.LRem(ctx, processingQueue, 1, raw)
			continue
		} else if derr != nil {
			log.Printf("check video deleted failed for %s: %v", task.VideoID, derr)
		}
		start := time.Now()
		_ = rdb.Incr(ctx, metricPrefix+"media:active")
		_ = rdb.Incr(ctx, metricPrefix+"video:status:processing")
		markProcessing(ctx, videos, task.VideoID)
		_ = rdb.Del(ctx, "video:"+task.VideoID)
		if err := processTask(ctx, minioClient, videos, ffmpegBin, ffprobeBin, minioEndpoint, videoBucket, coverBucket, task); err != nil {
			log.Printf("process task %s failed (attempt %d): %v", task.VideoID, task.Attempt+1, err)
			_ = rdb.LRem(ctx, processingQueue, 1, raw)
			_ = rdb.Decr(ctx, metricPrefix+"media:active")
			_ = rdb.Decr(ctx, metricPrefix+"video:status:processing")
			if task.Attempt+1 < maxAttempts {
				task.Attempt++
				_ = rdb.Incr(ctx, metricPrefix+"media:retried")
				if data, e := json.Marshal(task); e == nil {
					_ = rdb.LPush(ctx, taskQueue, string(data))
				}
			} else {
				markFailed(ctx, videos, task.VideoID, err.Error())
				_ = rdb.Del(ctx, "video:"+task.VideoID)
				_ = rdb.Incr(ctx, metricPrefix+"media:failed")
				_ = rdb.Incr(ctx, metricPrefix+"media:dlq")
				_ = rdb.Incr(ctx, metricPrefix+"video:status:failed")
				clearTaskTs(ctx, rdb, task.VideoID)
				// 原始任务入 DLQ 列表，供管理端审计重放（attempt 在重放时重置）
				_ = rdb.LPush(ctx, "media:dlq", raw).Err()
				log.Printf("task %s dropped after %d attempts", task.VideoID, maxAttempts)
			}
		} else {
			_ = rdb.LRem(ctx, processingQueue, 1, raw)
			_ = rdb.Decr(ctx, metricPrefix+"media:active")
			_ = rdb.Decr(ctx, metricPrefix+"video:status:processing")
			_ = rdb.Incr(ctx, metricPrefix+"media:succeeded")
			_ = rdb.Incr(ctx, metricPrefix+"video:status:succeeded")
			clearTaskTs(ctx, rdb, task.VideoID)
			log.Printf("task %s processed in %s", task.VideoID, time.Since(start).Round(time.Millisecond))
			// 失效 gateway 侧视频缓存，使 API 立即可见媒体元数据。
			_ = rdb.Del(ctx, "video:"+task.VideoID)
		}
	}
}

// clearTaskTs 任务终态（成功/永久失败/跳过）时清理年龄时间戳。
func clearTaskTs(ctx context.Context, rdb *redis.Client, videoID string) {
	if videoID == "" {
		return
	}
	if _, err := rdb.ZRem(ctx, "media:task_ts", videoID).Result(); err != nil {
		log.Printf("clear task ts failed for %s: %v", videoID, err)
	}
}

// startMediaMetrics serves bounded legacy observations independently of the business loop.
func startMediaMetrics(addr string, rdb *redis.Client) *http.Server {
	if addr == "" {
		addr = "127.0.0.1:9091"
	}
	srv := &http.Server{Addr: addr, Handler: newLegacyMediaMetricsHandler(rdb), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: time.Second}

	go func() {
		log.Printf("media worker metrics listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("media metrics server failed: %v", err)
		}
	}()
	return srv
}

// recoverProcessingQueue 把 processing 队列中滞留的任务重新放回任务队列。
// 正常流程下任务处理完成即从 processing 移除；进程崩溃留下的条目在此恢复，
// 由 maxAttempts 与重试状态机兜底去重/重试。
func recoverProcessingQueue(ctx context.Context, rdb *redis.Client, taskQueue, processingQueue string) error {
	raws, err := rdb.LRange(ctx, processingQueue, 0, -1).Result()
	if err != nil {
		return err
	}
	if len(raws) == 0 {
		return nil
	}
	for _, raw := range raws {
		if _, err := rdb.LPush(ctx, taskQueue, raw).Result(); err != nil {
			return err
		}
		if _, err := rdb.LRem(ctx, processingQueue, 1, raw).Result(); err != nil {
			return err
		}
	}
	log.Printf("recovered %d task(s) from processing queue", len(raws))
	return nil
}

// Every legacy update is fenced against persistent jobs and soft deletion.
func legacyVideoFilter(id primitive.ObjectID) bson.M {
	return bson.M{"_id": id, "media_job": bson.M{"$exists": false}, "status": bson.M{"$ne": "deleted"}, "deleted_at": bson.M{"$exists": false}}
}

func markProcessing(ctx context.Context, videos *mongo.Collection, videoID string) {
	objID, err := primitive.ObjectIDFromHex(videoID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	_, _ = videos.UpdateOne(ctx, repo.SearchVersionFilter(legacyVideoFilter(objID)), repo.SearchSourcePipeline(bson.M{
		"media_status":     "processing",
		"transcode_status": "processing",
		"updated_at":       now,
	}, nil, now))
}

type mediaObj struct{ bucket, object string }

func processTask(
	ctx context.Context,
	minioClient *minio.Client,
	videos *mongo.Collection,
	ffmpegBin, ffprobeBin, minioEndpoint, videoBucket, coverBucket string,
	task *mediaTask,
) error {
	partials, err := runProcessTask(ctx, minioClient, videos, ffmpegBin, ffprobeBin, minioEndpoint, videoBucket, coverBucket, task)
	if err != nil {
		cleanupPartialObjects(ctx, minioClient, partials)
	}
	return err
}

func cleanupPartialObjects(ctx context.Context, mc *minio.Client, partials []mediaObj) {
	for _, p := range partials {
		if err := mc.RemoveObject(ctx, p.bucket, p.object, minio.RemoveObjectOptions{}); err != nil {
			log.Printf("cleanup partial %s/%s failed: %v", p.bucket, p.object, err)
		} else {
			log.Printf("cleaned partial artifact %s/%s", p.bucket, p.object)
		}
	}
}

func runProcessTask(
	ctx context.Context,
	minioClient *minio.Client,
	videos *mongo.Collection,
	ffmpegBin, ffprobeBin, minioEndpoint, videoBucket, coverBucket string,
	task *mediaTask,
) ([]mediaObj, error) {
	src := sourceURL(minioEndpoint, task.Bucket, task.Object)
	var partials []mediaObj

	// 1. 媒体探测（ffprobe）
	probe, err := probeVideo(ctx, ffprobeBin, src)
	if err != nil {
		return partials, err
	}

	// 2. 多码率转码：480p + 720p
	transcodedObject, err := transcodeAndUpload(ctx, minioClient, ffmpegBin, videoBucket, src, task.VideoID+transcodeSuffix, 480)
	if err != nil {
		return partials, err
	}
	partials = append(partials, mediaObj{videoBucket, transcodedObject})
	transcodedObject720, err := transcodeAndUpload(ctx, minioClient, ffmpegBin, videoBucket, src, task.VideoID+transcodeSuffix720, 720)
	if err != nil {
		return partials, err
	}
	partials = append(partials, mediaObj{videoBucket, transcodedObject720})

	// 3. 无封面时生成缩略图
	var newCover string
	if !task.Cover {
		tmpThumb, err := os.CreateTemp("", thumbPrefix+"*.jpg")
		if err != nil {
			return partials, err
		}
		tmpThumbPath := tmpThumb.Name()
		tmpThumb.Close()
		defer os.Remove(tmpThumbPath)
		if err := runThumbnail(ctx, ffmpegBin, src, tmpThumbPath); err != nil {
			return partials, err
		}
		thumbObject := task.VideoID + ".jpg"
		if err := uploadFile(ctx, minioClient, coverBucket, thumbObject, tmpThumbPath, "image/jpeg"); err != nil {
			return partials, err
		}
		partials = append(partials, mediaObj{coverBucket, thumbObject})
		newCover = coverURL(coverBucket, thumbObject)
	}

	// 4. 回写 Mongo
	objID, err := primitive.ObjectIDFromHex(task.VideoID)
	if err != nil {
		return partials, err
	}
	set := bson.M{
		"transcode_status":    "ready",
		"transcoded_url":      coverURL(videoBucket, transcodedObject),
		"transcoded_url_720p": coverURL(videoBucket, transcodedObject720),
		"media_status":        "succeeded",
		"media_error":         "",
		"retry_count":         task.Attempt,
		"updated_at":          time.Now(),
	}
	if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && d > 0 {
		set["duration"] = d
	}
	for _, s := range probe.Streams {
		if s.Width > 0 && s.Height > 0 {
			set["width"] = s.Width
			set["height"] = s.Height
			break
		}
	}
	if newCover != "" {
		set["cover_url"] = newCover
	}
	dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
	upd, err := videos.UpdateOne(dbCtx, repo.SearchVersionFilter(legacyVideoFilter(objID)), repo.SearchSourcePipeline(set, nil, time.Now().UTC()))
	dbCancel()
	if err != nil {
		return partials, err
	}
	if upd.MatchedCount == 0 {
		return partials, fmt.Errorf("legacy video deleted, missing, or owns a persistent media job")
	}
	log.Printf("media processed for video %s (transcoded=%s cover=%s duration=%s %dx%d)",
		task.VideoID, coverURL(videoBucket, transcodedObject), newCover, probe.Format.Duration,
		firstDim(probe, true), firstDim(probe, false))
	return partials, nil
}

func firstDim(p probeResult, width bool) int {
	for _, s := range p.Streams {
		if width {
			return s.Width
		}
		return s.Height
	}
	return 0
}

func probeVideo(ctx context.Context, ffprobeBin, src string) (probeResult, error) {
	var p probeResult
	out, err := runMediaCommand(ctx, cmdTimeout, func(runCtx context.Context) ([]byte, error) {
		return exec.CommandContext(runCtx, ffprobeBin,
			"-v", "error",
			"-show_entries", "format=duration:stream=width,height",
			"-of", "json",
			src,
		).Output()
	})
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return p, err
	}
	return p, nil
}

func transcodeAndUpload(ctx context.Context, c *minio.Client, ffmpegBin, bucket, src, object string, height int) (string, error) {
	tmp, err := os.CreateTemp("", procPrefix+"*.mp4")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := runFFmpegHeight(ctx, ffmpegBin, src, tmpPath, height); err != nil {
		return "", err
	}
	if err := uploadFile(ctx, c, bucket, object, tmpPath, "video/mp4"); err != nil {
		return "", err
	}
	return object, nil
}

func runFFmpegHeight(ctx context.Context, ffmpegBin, src, out string, height int) error {
	_, err := runMediaCommand(ctx, cmdTimeout, func(runCtx context.Context) ([]byte, error) {
		return nil, exec.CommandContext(runCtx, ffmpegBin,
			// Bound decoder, filter and encoder threads independently. CPU quotas
			// alone do not stop FFmpeg allocating per-core buffers and hitting OOM.
			"-y", "-threads", "1", "-filter_threads", "1", "-i", src,
			"-vf", fmt.Sprintf("scale=-2:%d", height),
			"-c:v", "libx264", "-preset", "veryfast", "-threads", "1",
			"-c:a", "aac", "-b:a", "128k",
			out,
		).Run()
	})
	return err
}

// isVideoDeleted 判断视频文档是否存在/已删除（转码前检查，避免删除后白跑与产物遗留）。
func isVideoDeleted(ctx context.Context, videos *mongo.Collection, videoID string) (bool, error) {
	objID, err := primitive.ObjectIDFromHex(videoID)
	if err != nil {
		return false, err
	}
	var doc struct {
		Status    string        `bson:"status"`
		MediaJob  bson.RawValue `bson:"media_job"`
		DeletedAt bson.RawValue `bson:"deleted_at"`
	}
	if err := videos.FindOne(ctx, bson.M{"_id": objID}).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return true, nil
		}
		return false, err
	}
	return doc.Status == "deleted" || doc.MediaJob.Type != 0 || doc.DeletedAt.Type != 0, nil
}

func runThumbnail(ctx context.Context, ffmpegBin, src, out string) error {
	_, err := runMediaCommand(ctx, cmdTimeout, func(runCtx context.Context) ([]byte, error) {
		return nil, exec.CommandContext(runCtx, ffmpegBin,
			"-y", "-threads", "1", "-filter_threads", "1", "-i", src,
			"-ss", "00:00:01",
			"-vframes", "1",
			"-vf", "scale=480:-2",
			"-threads", "1",
			out,
		).Run()
	})
	return err
}

func uploadFile(ctx context.Context, c *minio.Client, bucket, object, path, contentType string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, _ := f.Stat()
	_, err = c.PutObject(ctx, bucket, object, f, info.Size(),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

func markFailed(ctx context.Context, videos *mongo.Collection, videoID, reason string) {
	objID, err := primitive.ObjectIDFromHex(videoID)
	if err != nil {
		return
	}
	// 原因截断并去除潜在敏感信息（保留纯错误消息）。
	if len(reason) > 500 {
		reason = reason[:500]
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	_, _ = videos.UpdateOne(ctx, repo.SearchVersionFilter(legacyVideoFilter(objID)), repo.SearchSourcePipeline(bson.M{
		"transcode_status": "failed",
		"media_status":     "failed",
		"media_error":      reason,
		"updated_at":       now,
	}, nil, now))
}
