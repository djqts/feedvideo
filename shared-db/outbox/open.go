package outbox

import "gorm.io/gorm"

// OpenStore attaches to a validated schema without executing DDL.
func OpenStore(db *gorm.DB) *Store { return &Store{db: db} }
