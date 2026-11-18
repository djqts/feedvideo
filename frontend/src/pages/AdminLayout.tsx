import React from 'react'
import { Outlet } from 'react-router-dom'
import AdminSidebar from '../components/AdminSidebar'

export default function AdminLayout() {
  return (
    <div className="flex min-h-screen">
      <AdminSidebar />
      <main id="main-content" tabIndex={-1} className="flex-1 p-6 pt-20 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}
