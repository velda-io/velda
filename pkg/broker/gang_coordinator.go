package broker

import "sync"

type GangCoordinator struct {
	desired int
	mu      sync.Mutex
	notif   map[int]func()
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
	if len(g.notif) == g.desired {
		for _, fn := range g.notif {
			fn()
		}
		g.notif = nil
	}
}

func (g *GangCoordinator) Unnotify(id int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.notif == nil {
		return false
	}
	delete(g.notif, id)
	return true
}
