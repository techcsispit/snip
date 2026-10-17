package main

import (
	"path/filepath"
	"testing"
)

func TestStorePathAllowsOnlyOneOpenStore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "links.json")

	first, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	defer first.Close()

	second, err := OpenStore(storePath)
	if err == nil {
		second.Close()
		t.Fatal("opened the same store twice; want an exclusive-use error")
	}
}

func TestStorePathCanBeReopenedAfterClose(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "links.json")

	first, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	first.Close()

	second, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("reopen store after close: %v", err)
	}
	second.Close()
}

func TestDifferentStorePathsCanBeOpenedTogether(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenStore(filepath.Join(dir, "first.json"))
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	defer first.Close()

	second, err := OpenStore(filepath.Join(dir, "second.json"))
	if err != nil {
		t.Fatalf("open different store: %v", err)
	}
	second.Close()
}
