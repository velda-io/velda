package broker

import "sync"

type GangCoordinator struct {
	mu        sync.Mutex
	desired   int
	notif     map[int]func()
	triggered bool
}

func NewGangCoordinator(desired int) *GangCoordinator {
	return &GangCoordinator{
		desired: desired,
		notif:   make(map[int]func()),
	}
}

func (g *GangCoordinator) Notify(id int, f func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.notif[id] = f
	if len(g.notif) == g.desired && !g.triggered {
		for _, fn := range g.notif {
			fn()
		}
		g.triggered = true
	}
}

func (g *GangCoordinator) Unnotify(id int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.triggered {
		return false
	}
	delete(g.notif, id)
	return true
}
