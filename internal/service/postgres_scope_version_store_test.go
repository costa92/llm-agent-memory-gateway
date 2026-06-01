package service

import "testing"

func TestNewPostgresScopeVersionStore(t *testing.T) {
	store := NewPostgresScopeVersionStore(nil)
	if store == nil {
		t.Fatal("store is nil")
	}
}
