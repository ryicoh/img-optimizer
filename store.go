package main

import (
	"errors"
	"io"
	"os"
	"sync"
)

var errKeyNotFound = errors.New("key not found")

type (
	store struct {
		m   sync.Map
	}
)

func newStore(dir string) *store {
	return &store{}
}

func (fc *store) get(path string) (io.ReadCloser, error) {
	v, _ := fc.m.LoadOrStore(path, &sync.RWMutex{})
	m := v.(*sync.RWMutex)
	m.RLock()
	defer m.RUnlock()

	if _, err := os.Stat(path); err != nil {
		return nil, errKeyNotFound
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, errKeyNotFound
	}

	return f, nil
}

func (fc *store) set(key string, f func() error) error {
	v, _ := fc.m.LoadOrStore(key, &sync.RWMutex{})
	m := v.(*sync.RWMutex)
	m.RLock()
	defer m.RUnlock()

	return f()
}
