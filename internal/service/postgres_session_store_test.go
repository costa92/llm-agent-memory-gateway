package service

import "testing"

func TestNewPostgresSessionStateStore(t *testing.T) {
	store := NewPostgresSessionStateStore(nil)
	if store == nil {
		t.Fatal("store is nil")
	}
}
