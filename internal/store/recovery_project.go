package store

import "errors"

// ValidateProject inspects surviving explicit identities without cloning the
// retained evidence. Compacted hashes alone cannot prove a project's identity.
func (s *Store) ValidateProject(project string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	observed := false
	for _, row := range s.rows {
		if row.Project != "" {
			observed = true
			if row.Project != project {
				return false, errors.New("backup records belong to another project")
			}
		}
	}
	for _, beat := range s.beats {
		if beat.Project != "" {
			observed = true
			if beat.Project != project {
				return false, errors.New("backup heartbeat belongs to another project")
			}
		}
	}
	return observed, nil
}
