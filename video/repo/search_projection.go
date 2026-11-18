package repo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var (
	ErrSearchProjectionInvalid     = errors.New("invalid search projection")
	ErrSearchProjectionUnavailable = errors.New("search projection unavailable")
)

type SearchProjectionStore interface {
	Claim(context.Context, string, time.Time, time.Duration) (*model.SearchProjectionLease, error)
	Complete(context.Context, *model.SearchProjectionLease, time.Time) (bool, error)
	Fail(context.Context, *model.SearchProjectionLease, time.Time, string) (bool, error)
	Pending(context.Context) (int64, time.Time, error)
	EnsureIndexes(context.Context) error
}

type searchProjectionRepo struct{ db *mongo.Database }

func NewSearchProjectionRepo(db *mongo.Database) SearchProjectionStore {
	return &searchProjectionRepo{db: db}
}

func (r *searchProjectionRepo) Claim(ctx context.Context, owner string, now time.Time, ttl time.Duration) (*model.SearchProjectionLease, error) {
	if owner == "" || now.IsZero() || ttl < time.Millisecond || ttl > time.Minute {
		return nil, ErrSearchProjectionInvalid
	}
	if r == nil || r.db == nil {
		return nil, ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now = now.UTC().Truncate(time.Millisecond)
	until := now.Add(ttl).Truncate(time.Millisecond)
	filter := bson.M{
		"version":                           bson.M{"$type": bson.A{"int", "long"}, "$gt": int64(0)},
		"search_projection.dirty":           true,
		"search_projection.lease_token":     bson.M{"$type": bson.A{"int", "long"}, "$gte": int64(0), "$lt": int64(math.MaxInt64)},
		"search_projection.attempts":        bson.M{"$type": bson.A{"int", "long"}, "$gte": int64(0), "$lt": int64(math.MaxInt64)},
		"search_projection.next_attempt_at": bson.M{"$lte": now},
		"search_projection.lease_until":     bson.M{"$lte": now},
		"$expr":                             bson.M{"$eq": bson.A{"$search_projection.source_version", "$version"}},
		// Keep the index-friendly eligibility predicates and source equality,
		// but reject non-scalar fields before the atomic counter increments.
		"$and": bson.A{bson.M{"$expr": bson.M{"$and": bson.A{
			searchProjectionIntegerTypeExpression("version"),
			searchProjectionIntegerTypeExpression("search_projection.source_version"),
			searchProjectionIntegerTypeExpression("search_projection.lease_token"),
			searchProjectionIntegerTypeExpression("search_projection.attempts"),
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.dirty"}, "bool"}},
			bson.M{"$eq": bson.A{"$search_projection.dirty", true}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.next_attempt_at"}, "date"}},
			bson.M{"$lte": bson.A{"$search_projection.next_attempt_at", now}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.lease_until"}, "date"}},
			bson.M{"$lte": bson.A{"$search_projection.lease_until", now}},
		}}}},
	}
	command := bson.D{
		{Key: "findAndModify", Value: "videos"}, {Key: "query", Value: filter},
		{Key: "sort", Value: bson.D{{Key: "search_projection.next_attempt_at", Value: 1}, {Key: "_id", Value: 1}}},
		{Key: "update", Value: bson.M{
			"$inc": bson.M{"search_projection.lease_token": int64(1), "search_projection.attempts": int64(1)},
			"$set": bson.M{"search_projection.lease_owner": owner, "search_projection.lease_until": until},
		}},
		{Key: "new", Value: true}, {Key: "upsert", Value: false},
		{Key: "writeConcern", Value: bson.M{"w": 1}}, {Key: "maxTimeMS", Value: int64(5000)},
	}
	response, err := r.write(ctx, command)
	if err != nil {
		return nil, err
	}
	value := response.Lookup("value")
	if value.Type == bsontype.Null {
		return nil, nil
	}
	if value.Type != bsontype.EmbeddedDocument {
		return nil, searchProjectionUnavailable(errors.New("missing claimed source"))
	}
	raw := value.Document()
	var video model.Video
	if err := bson.Unmarshal(raw, &video); err != nil {
		return nil, searchProjectionUnavailable(err)
	}
	projection := video.SearchProjection
	version, versionOK := searchProjectionInteger(raw.Lookup("version"))
	source, sourceOK := searchProjectionInteger(raw.Lookup("search_projection", "source_version"))
	token, tokenOK := searchProjectionInteger(raw.Lookup("search_projection", "lease_token"))
	attempt, attemptOK := searchProjectionInteger(raw.Lookup("search_projection", "attempts"))
	if video.ID == primitive.NilObjectID || projection == nil || !versionOK || !sourceOK || !tokenOK || !attemptOK || version < 1 || source != version || token < 1 || attempt < 1 || !projection.Dirty || projection.LeaseOwner != owner || !projection.LeaseUntil.Equal(until) || !projection.LeaseUntil.After(now) {
		return nil, searchProjectionUnavailable(errors.New("invalid claimed source lease"))
	}
	return &model.SearchProjectionLease{Video: &video, SourceVersion: source, Token: token, Attempt: attempt, Owner: owner, Until: projection.LeaseUntil}, nil
}

func searchProjectionUnavailable(cause error) error {
	return fmt.Errorf("%w: %w", ErrSearchProjectionUnavailable, cause)
}

func searchProjectionInteger(value bson.RawValue) (int64, bool) {
	if value.Type != bsontype.Int32 && value.Type != bsontype.Int64 {
		return 0, false
	}
	return value.AsInt64(), true
}

// RunCommand does not enable the driver's automatic read/write retries. An unknown
// write result remains unknown; the worker may recover only through a fresh lease.
func (r *searchProjectionRepo) write(ctx context.Context, command bson.D) (bson.Raw, error) {
	response, err := r.db.RunCommand(ctx, command, options.RunCmd().SetReadPreference(readpref.Primary())).Raw()
	if err != nil {
		return nil, searchProjectionUnavailable(err)
	}
	ok := response.Lookup("ok")
	if (ok.Type != bsontype.Double || ok.Double() != 1) && (ok.Type != bsontype.Int32 && ok.Type != bsontype.Int64 || ok.AsInt64() != 1) {
		return nil, searchProjectionUnavailable(errors.New("unconfirmed Mongo command"))
	}
	if value := response.Lookup("writeConcernError"); value.Type != 0 && value.Type != bsontype.Null {
		return nil, searchProjectionUnavailable(errors.New("unconfirmed Mongo write concern"))
	}
	if value := response.Lookup("writeErrors"); value.Type != 0 {
		if value.Type != bsontype.Array {
			return nil, searchProjectionUnavailable(errors.New("invalid Mongo write result"))
		}
		entries, err := value.Array().Values()
		if err != nil || len(entries) != 0 {
			return nil, searchProjectionUnavailable(errors.New("Mongo write failed"))
		}
	}
	return response, nil
}

func (r *searchProjectionRepo) Complete(ctx context.Context, lease *model.SearchProjectionLease, now time.Time) (bool, error) {
	if !validSearchProjectionLease(lease) || now.IsZero() {
		return false, ErrSearchProjectionInvalid
	}
	if !lease.Until.After(now) {
		return false, nil
	}
	if r == nil || r.db == nil {
		return false, ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.update(ctx, searchProjectionCAS(lease, now), bson.M{
		"search_projection.applied_version": lease.SourceVersion,
		"search_projection.dirty":           false,
		"search_projection.lease_owner":     "",
		"search_projection.lease_until":     time.Time{},
		"search_projection.last_error_code": "",
	})
}

func (r *searchProjectionRepo) Fail(ctx context.Context, lease *model.SearchProjectionLease, now time.Time, code string) (bool, error) {
	if !validSearchProjectionLease(lease) || now.IsZero() {
		return false, ErrSearchProjectionInvalid
	}
	if !lease.Until.After(now) {
		return false, nil
	}
	if r == nil || r.db == nil {
		return false, ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch code {
	case "es_unavailable", "version_conflict", "internal_error":
	default:
		code = "internal_error"
	}
	return r.update(ctx, searchProjectionCAS(lease, now), bson.M{
		"search_projection.next_attempt_at": now.UTC().Add(searchProjectionBackoff(lease.Attempt)),
		"search_projection.lease_owner":     "",
		"search_projection.lease_until":     time.Time{},
		"search_projection.last_error_code": code,
	})
}

func validSearchProjectionLease(lease *model.SearchProjectionLease) bool {
	return lease != nil && lease.Video != nil && lease.Video.ID != primitive.NilObjectID && lease.SourceVersion > 0 && lease.Video.Version == lease.SourceVersion && lease.Token > 0 && lease.Attempt > 0 && lease.Owner != "" && !lease.Until.IsZero()
}

func searchProjectionIntegerTypeExpression(field string) bson.M {
	return bson.M{"$in": bson.A{bson.M{"$type": "$" + field}, bson.A{"int", "long"}}}
}

func searchProjectionCAS(lease *model.SearchProjectionLease, now time.Time) bson.M {
	return bson.M{
		"_id": lease.Video.ID, "version": lease.SourceVersion,
		"search_projection.source_version": lease.SourceVersion,
		"search_projection.dirty":          true,
		"search_projection.lease_owner":    lease.Owner,
		"search_projection.lease_token":    lease.Token,
		"search_projection.lease_until":    bson.M{"$gt": now.UTC()},
		// Query equality may match array elements (including an old token).
		// Check the current DB fields with typed aggregation equality as well.
		"$expr": bson.M{"$and": bson.A{
			searchProjectionIntegerTypeExpression("version"),
			searchProjectionIntegerTypeExpression("search_projection.source_version"),
			searchProjectionIntegerTypeExpression("search_projection.lease_token"),
			bson.M{"$eq": bson.A{"$version", lease.SourceVersion}},
			bson.M{"$eq": bson.A{"$search_projection.source_version", lease.SourceVersion}},
			bson.M{"$eq": bson.A{"$search_projection.lease_token", lease.Token}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.lease_owner"}, "string"}},
			bson.M{"$eq": bson.A{"$search_projection.lease_owner", bson.M{"$literal": lease.Owner}}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.dirty"}, "bool"}},
			bson.M{"$eq": bson.A{"$search_projection.dirty", true}},
			bson.M{"$eq": bson.A{bson.M{"$type": "$search_projection.lease_until"}, "date"}},
			bson.M{"$gt": bson.A{"$search_projection.lease_until", now.UTC()}},
		}},
	}
}

func searchProjectionBackoff(attempt int64) time.Duration {
	var base time.Duration
	switch attempt {
	case 1:
		base = time.Second
	case 2:
		base = 5 * time.Second
	case 3:
		base = 30 * time.Second
	case 4:
		base = time.Minute
	case 5:
		base = 2 * time.Minute
	case 6:
		base = 4 * time.Minute
	default:
		return 5 * time.Minute
	}
	return base + time.Duration(rand.Int64N(int64(base/5)+1))
}

func (r *searchProjectionRepo) update(ctx context.Context, filter, set bson.M) (bool, error) {
	command := bson.D{
		{Key: "update", Value: "videos"},
		{Key: "updates", Value: bson.A{bson.M{"q": filter, "u": bson.M{"$set": set}, "multi": false, "upsert": false}}},
		{Key: "ordered", Value: true}, {Key: "writeConcern", Value: bson.M{"w": 1}},
	}
	response, err := r.write(ctx, command)
	if err != nil {
		return false, err
	}
	n, ok := searchProjectionInteger(response.Lookup("n"))
	if !ok || n < 0 || n > 1 {
		return false, searchProjectionUnavailable(errors.New("invalid Mongo CAS match count"))
	}
	return n == 1, nil
}

func (r *searchProjectionRepo) Pending(ctx context.Context) (int64, time.Time, error) {
	if r == nil || r.db == nil {
		return 0, time.Time{}, ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	col := r.db.Collection("videos")
	filter := bson.M{"search_projection.dirty": true}
	n, err := col.CountDocuments(ctx, filter, options.Count().SetMaxTime(5*time.Second))
	if err != nil {
		return 0, time.Time{}, searchProjectionUnavailable(err)
	}
	if n < 0 {
		return 0, time.Time{}, searchProjectionUnavailable(errors.New("invalid pending count"))
	}
	raw, err := col.FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"search_projection.dirty_since": 1}).SetSort(bson.D{{Key: "search_projection.dirty_since", Value: 1}, {Key: "_id", Value: 1}}).SetMaxTime(5*time.Second)).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return n, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, searchProjectionUnavailable(err)
	}
	value := raw.Lookup("search_projection", "dirty_since")
	if value.Type != bsontype.DateTime {
		return 0, time.Time{}, searchProjectionUnavailable(errors.New("invalid oldest pending time"))
	}
	since := time.UnixMilli(value.DateTime()).UTC()
	if since.IsZero() {
		return 0, time.Time{}, searchProjectionUnavailable(errors.New("invalid oldest pending time"))
	}
	return n, since, nil
}

func (r *searchProjectionRepo) EnsureIndexes(ctx context.Context) error {
	if r == nil || r.db == nil {
		return ErrSearchProjectionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := r.write(ctx, bson.D{
		{Key: "createIndexes", Value: "videos"},
		{Key: "indexes", Value: bson.A{
			bson.M{"name": "search_projection_due", "key": bson.D{{Key: "search_projection.dirty", Value: 1}, {Key: "search_projection.next_attempt_at", Value: 1}, {Key: "search_projection.lease_until", Value: 1}, {Key: "_id", Value: 1}}},
			bson.M{"name": "search_projection_age", "key": bson.D{{Key: "search_projection.dirty", Value: 1}, {Key: "search_projection.dirty_since", Value: 1}, {Key: "_id", Value: 1}}},
		}},
		{Key: "writeConcern", Value: bson.M{"w": 1}}, {Key: "maxTimeMS", Value: int64(5000)},
	})
	return err
}
