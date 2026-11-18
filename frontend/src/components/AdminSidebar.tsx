import React from 'react'
import { NavLink } from 'react-router-dom'

const navItems = [
  { label: '举报管理', to: '/admin/reports' },
  { label: '视频审核', to: '/admin/videos' },
  { label: '用户管理', to: '/admin/users' },
  { label: '平台统计', to: '/admin/stats' },
]

export default function AdminSidebar() {
  return (
    <aside className="mb-8 border-b border-border pb-5">
      <div>
        <p className="ef-eyebrow mb-2">// ADMINISTRATION</p>
        <h2 className="text-sm font-semibold mb-4">
          管理后台
        </h2>
        <nav aria-label="管理后台" className="flex flex-wrap gap-2">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end
              className={({ isActive }) =>
                `ef-cut inline-flex min-h-12 items-center border px-5 py-2 text-sm transition-colors ${
                  isActive
                    ? 'border-accent bg-accent text-accent-foreground font-bold'
                    : 'border-border bg-card text-muted-foreground hover:bg-muted hover:text-foreground'
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </div>
    </aside>
  )
}
