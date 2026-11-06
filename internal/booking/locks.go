package booking

import "sync"

// Locks serialises booking for one API process: patient first, then line.
type Locks struct {
	mu       sync.Mutex
	patients map[int64]*sync.Mutex
	lines    map[int64]*sync.Mutex
}

func (l *Locks) key(m *map[int64]*sync.Mutex, id int64) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if *m == nil {
		*m = map[int64]*sync.Mutex{}
	}
	mu := (*m)[id]
	if mu == nil {
		mu = &sync.Mutex{}
		(*m)[id] = mu
	}
	return mu
}

func (l *Locks) Patient(id int64) func() {
	mu := l.key(&l.patients, id)
	mu.Lock()
	return mu.Unlock
}

func (l *Locks) Line(id int64) func() {
	mu := l.key(&l.lines, id)
	mu.Lock()
	return mu.Unlock
}
