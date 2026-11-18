[CmdletBinding()]
param(
  [string]$OutputDirectory = $PSScriptRoot,
  [ValidateSet('development', 'production')][string]$Environment = 'development'
)
$ErrorActionPreference = 'Stop'

if ($Environment -ne 'development' -or $env:APP_ENV -eq 'production') {
  throw 'Development initialization is disabled for production. Inject independent production credentials externally.'
}
$targetDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$targetFile = Join-Path $targetDirectory '.env'
if (Test-Path -LiteralPath $targetFile) {
  foreach ($line in [IO.File]::ReadLines($targetFile)) {
    if ($line -match '^\s*APP_ENV\s*=\s*["'']?production["'']?\s*$') {
      throw 'Existing production configuration is preserved; development initialization is refused.'
    }
  }
  Write-Host 'Existing .env preserved.'
  exit 0
}

function New-DevelopmentSecret {
  return [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant()
}
$mysqlSecret = New-DevelopmentSecret
$mongoSecret = New-DevelopmentSecret
$redisSecret = New-DevelopmentSecret
$rabbitSecret = New-DevelopmentSecret
$minioSecret = New-DevelopmentSecret
$jwtSecret = New-DevelopmentSecret
$content = @"
# Generated for development only. Do not commit this file.
APP_ENV=development
MIGRATIONS_MODE=apply
DB_AUTO_MIGRATE=true
MYSQL_PASSWORD=$mysqlSecret
MYSQL_DATABASE=feedvideo
MYSQL_USER=root
MONGO_INITDB_ROOT_USERNAME=feedvideo_dev
MONGO_INITDB_ROOT_PASSWORD=$mongoSecret
REDIS_PASSWORD=$redisSecret
RABBITMQ_DEFAULT_USER=feedvideo_dev
RABBITMQ_DEFAULT_PASS=$rabbitSecret
MINIO_ROOT_USER=feedvideo_dev
MINIO_ROOT_PASSWORD=$minioSecret
JWT_SECRET_KEY=$jwtSecret
PRIVATE_KEY_PATH=/app/keys/private.pem
PUBLIC_KEY_PATH=/app/keys/public.pem
ADMIN_IDS=
CORS_ALLOWED_ORIGINS=
VIDEO_BUCKET=feedvideo-videos
COVER_BUCKET=feedvideo-covers
NGINX_HTTP_PORT=80
NGINX_HTTPS_PORT=443
"@
$null = New-Item -ItemType Directory -Path $targetDirectory -Force
$fileStream = $null
try {
  # CreateNew protects an existing file even if another initializer won the race.
  $fileStream = [IO.File]::Open($targetFile, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
  $bytes = [Text.UTF8Encoding]::new($false).GetBytes($content + [Environment]::NewLine)
  $fileStream.Write($bytes, 0, $bytes.Length)
} finally {
  if ($null -ne $fileStream) { $fileStream.Dispose() }
}
if (-not $IsWindows) {
  [IO.File]::SetUnixFileMode($targetFile, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite)
}
Write-Host 'Created development .env with independent random secrets.'
