package outbox

import "slices"

func (q *Queue) consolidateRecipients(primary string, aliases []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.compactAcks(); err != nil {
		return err
	}
	for id, meta := range q.items {
		found := false
		for _, rid := range meta.Pending {
			found = found || slices.Contains(aliases, rid)
		}
		if !found {
			continue
		}
		v, err := q.loadItem(id)
		if err != nil {
			return err
		}
		v.Pending = slices.DeleteFunc(v.Pending, func(rid string) bool { return slices.Contains(aliases, rid) })
		if !slices.Contains(v.Pending, primary) {
			v.Pending = append(v.Pending, primary)
		}
		if err = q.write(v); err != nil {
			return err
		}
	}
	return nil
}
