try {
  const dark = localStorage.getItem('feedvideo-theme') === 'dark'
  document.documentElement.classList.toggle('dark', dark)
  document.querySelector('meta[name="theme-color"]').content = dark ? '#1b1a17' : '#f3f1ec'
} catch { /* Use the default light theme if storage is unavailable. */ }
