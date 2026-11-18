package model

import "gorm.io/gorm"

// AutoMigrate 迁移 user 模块的数据库表。
// 原位于 shared-db/sql/initDB.go，为解除 shared-db → user 的环依赖而迁入本包。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&User{},
	)
}
