import React from 'react'
import { Link } from 'react-router-dom'
import { Button } from '../components/ui/button'

export default function AccountDisabledPage() {
  return (
    <div className="text-center mt-24">
      <h1 className="text-7xl font-black text-destructive/20">已禁用</h1>
      <p className="text-muted-foreground mt-2 mb-6">该账号已被禁用，请联系管理员</p>
      <Link to="/login"><Button>返回登录</Button></Link>
    </div>
  )
}
