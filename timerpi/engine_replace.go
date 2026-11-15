package timerpi

// ReplaceShows excludes runtime writers and engine lookups while write replaces
// their database rows. Success retires the affected engines; failure preserves
// them. write must not call engines or publish to their subscribers.
func (r *Engines) ReplaceShows(write func() ([]int64, error)) error {
	r.admission.Lock()
	defer r.admission.Unlock()
	if r.admission.pending {
		return ErrUpdatePending
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	locked := make([]*Engine, 0, len(r.byShow))
	for _, e := range r.byShow {
		e.mu.Lock()
		locked = append(locked, e)
	}
	defer func() {
		for _, e := range locked {
			e.mu.Unlock()
		}
	}()
	ids, err := write()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if e := r.byShow[id]; e != nil {
			e.retired = true
			delete(r.byShow, id)
		}
	}
	return nil
}
