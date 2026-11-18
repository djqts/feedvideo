import { Bell, Bookmark, ChartNoAxesCombined, Clock3, Hash, House, Plus, Search, ShieldCheck, UserRound, UsersRound } from 'lucide-react'

export const mainNavigation = [
  { to: '/', label: '首页', icon: House },
  { to: '/search', label: '搜索', icon: Search },
  { to: '/topics', label: '话题广场', icon: Hash },
]
export const personalNavigation = [
  { to: '/notifications', label: '通知', icon: Bell },
  { to: '/collections', label: '我的收藏', icon: Bookmark },
  { to: '/history', label: '观看历史', icon: Clock3 },
  { to: '/friends', label: '好友', icon: UsersRound },
  { to: '/me', label: '我的', icon: UserRound },
]
export const workspaceNavigation = [
  { to: '/creator', label: '创作者中心', icon: ChartNoAxesCombined },
  { to: '/publish', label: '发布作品', icon: Plus },
]
export const adminNavigation = [{ to: '/admin/reports', label: '管理中心', icon: ShieldCheck }]
export const mobileNavigation = [
  mainNavigation[0],
  mainNavigation[1],
  { to: '/publish', label: '发布', icon: Plus },
  personalNavigation[0],
  { to: '/me', label: '我的', icon: UserRound },
]

export function isNavigationActive(pathname: string, to: string, uid: string | null) {
  if (to === '/') return pathname === '/'
  if (to === '/me' && uid && pathname.replace(/\/$/, '') === `/profile/${uid}`) return true
  if (to === '/admin/reports') return pathname.startsWith('/admin/')
  return pathname === to || pathname.startsWith(`${to}/`)
}
