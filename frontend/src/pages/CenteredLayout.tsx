import React from 'react'
import { Outlet } from 'react-router-dom'

export default function CenteredLayout() {
  return (
    <main id="main-content" tabIndex={-1} className="max-w-6xl mx-auto px-4 md:px-6 pt-20 pb-20">
      <Outlet />
    </main>
  )
}
