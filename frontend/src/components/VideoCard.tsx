import React from 'react'
import { Link } from 'react-router-dom'
import { Bookmark, Heart, Play, Share2 } from 'lucide-react'

export interface VideoCardProps {
  item: any
  onCollect: () => void
  onShare: () => void
  onLike: () => void
}

function VideoCardBase({ item, onCollect, onShare, onLike }: VideoCardProps) {
  const video = item.video || {}
  return (
    <article className="feed-card group min-w-0 bg-card">
      <Link to={`/videos/${video.id}`} className="block focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-foreground">
        <div className="relative w-full aspect-[3/4] overflow-hidden bg-muted">
          {video.cover_url ? (
            <img
              src={video.cover_url}
              alt=""
              loading="lazy"
              className="w-full h-full aspect-[3/4] object-cover"
            />
          ) : (
            <div className="w-full h-full flex flex-col items-center justify-center gap-3 text-muted-foreground">
              <Play className="size-9" strokeWidth={1.2} aria-hidden="true" />
              <span className="ef-eyebrow text-[10px]">VIDEO</span>
            </div>
          )}
        </div>
        <div className="px-2 pt-3 sm:px-3">
          <h2 className="min-h-10 text-sm font-semibold leading-5 line-clamp-2 group-hover:underline group-hover:underline-offset-4 sm:min-h-12 sm:text-base sm:leading-6">{video.title || '未命名视频'}</h2>
          <div className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
            <Play className="size-3" aria-hidden="true" />
            <span>{item.stats?.play_count || 0} 次播放</span>
          </div>
        </div>
      </Link>
      <div className="mx-2 mt-3 grid grid-cols-3 gap-0.5 border-t border-border py-1 text-xs text-muted-foreground sm:mx-3">
        <button
          type="button"
          aria-label={item.like_state_known === true ? "点赞" : "点赞暂不可用"}
          aria-pressed={item.like_state_known === true ? !!item.is_liked : undefined}
          onClick={() => { if (item.like_state_known === true) onLike() }}
          disabled={item.like_state_known !== true}
          className={`ef-cut min-h-12 min-w-0 px-1 flex flex-col sm:flex-row items-center justify-center gap-0.5 sm:gap-1.5 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-3px] focus-visible:outline-foreground ${item.is_liked ? 'bg-accent text-accent-foreground' : 'hover:bg-muted hover:text-foreground'}`}
        >
          <Heart className="size-4 shrink-0" fill={item.is_liked ? 'currentColor' : 'none'} aria-hidden="true" />
          <span className="ef-number">{item.like_state_known === true ? item.stats?.like_count || 0 : "点赞暂不可用"}</span>
        </button>
        <button
          type="button"
          aria-label="收藏"
          aria-pressed={!!item.is_collected}
          onClick={onCollect}
          className={`ef-cut min-h-12 min-w-0 px-1 flex flex-col sm:flex-row items-center justify-center gap-0.5 sm:gap-1.5 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-3px] focus-visible:outline-foreground ${item.is_collected ? 'bg-accent text-accent-foreground' : 'hover:bg-muted hover:text-foreground'}`}
        >
          <Bookmark className="size-4 shrink-0" fill={item.is_collected ? 'currentColor' : 'none'} aria-hidden="true" />
          <span className="ef-number">{item.collect_count || 0}</span>
        </button>
        <button
          type="button"
          aria-label="分享"
          onClick={onShare}
          className="ef-cut min-h-12 min-w-0 px-1 flex flex-col sm:flex-row items-center justify-center gap-0.5 sm:gap-1.5 hover:bg-muted hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-3px] focus-visible:outline-foreground"
        >
          <Share2 className="size-4 shrink-0" aria-hidden="true" />
          <span className="ef-number">{item.share_count || 0}</span>
        </button>
      </div>
    </article>
  )
}

// 自定义 memo 比较：仅当关键字段变化时重渲染
export function videoCardPropsAreEqual(prev: VideoCardProps, next: VideoCardProps): boolean {
  return (
    prev.item?.video?.id === next.item?.video?.id &&
    prev.item?.video?.title === next.item?.video?.title &&
    prev.item?.video?.cover_url === next.item?.video?.cover_url &&
    prev.item?.stats?.play_count === next.item?.stats?.play_count &&
    prev.item?.is_liked === next.item?.is_liked &&
    prev.item?.like_state_known === next.item?.like_state_known &&
    prev.item?.is_collected === next.item?.is_collected &&
    prev.item?.stats?.like_count === next.item?.stats?.like_count &&
    prev.item?.collect_count === next.item?.collect_count &&
    prev.item?.share_count === next.item?.share_count
  )
}

const VideoCard = React.memo(VideoCardBase, videoCardPropsAreEqual)
export default VideoCard
