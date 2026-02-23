package storage

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrDuplicate      = errors.New("duplicate entry")
	ErrConflict       = errors.New("version conflict")
	ErrInvalidInput   = errors.New("invalid input")
	ErrDatabaseClosed = errors.New("database is closed")
)
