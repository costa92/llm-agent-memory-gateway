package service

import (
	"reflect"
	"testing"

	corememory "github.com/costa92/llm-agent-memory-contract/contract"
	pgmemory "github.com/costa92/llm-agent-memory-postgres/postgres"
)

func TestConvertSessionWorkingRecords_Nil(t *testing.T) {
	if got := convertSessionWorkingRecords(nil); got != nil {
		t.Fatalf("nil input should yield nil slice, got %v", got)
	}
}

func TestConvertSessionWorkingRecords_Empty(t *testing.T) {
	got := convertSessionWorkingRecords([]pgmemory.SessionWorkingRecord{})
	if got == nil {
		t.Fatal("empty (non-nil) input should yield a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestConvertSessionWorkingRecords_PreservesFields(t *testing.T) {
	in := []pgmemory.SessionWorkingRecord{
		{
			Record:        corememory.MemoryRecord{MemoryID: "m1", TenantID: "t", Kind: corememory.RecordKindWorking, Version: 3},
			LatestEventID: "e1",
		},
		{
			Record:        corememory.MemoryRecord{MemoryID: "m2", TenantID: "t", Kind: corememory.RecordKindWorking, Version: 7},
			LatestEventID: "",
		},
	}
	got := convertSessionWorkingRecords(in)
	if len(got) != len(in) {
		t.Fatalf("len = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if !reflect.DeepEqual(got[i].Record, in[i].Record) {
			t.Fatalf("[%d] Record = %+v, want %+v", i, got[i].Record, in[i].Record)
		}
		if got[i].LatestEventID != in[i].LatestEventID {
			t.Fatalf("[%d] LatestEventID = %q, want %q", i, got[i].LatestEventID, in[i].LatestEventID)
		}
	}
}

func TestNewPostgresSessionWorkingStore_NilStoreYieldsNil(t *testing.T) {
	if got := newPostgresSessionWorkingStore(nil); got != nil {
		t.Fatalf("nil store should yield nil adapter, got %v", got)
	}
}
