package main

import "net/http"

func localRootAdministrator(r *http.Request) (session, bool) {
	uid, ok := r.Context().Value(peerUIDKey{}).(uint32)
	if !ok || uid != 0 {
		return session{}, false
	}
	return session{
		Username:   systemAdminUsername,
		Role:       roleAdministrator,
		AuthSource: authSourceLocalRoot,
	}, true
}

func (s *server) authorizeSession(w http.ResponseWriter, r *http.Request) (session, bool) {
	_, sess, ok := s.authorize(r, false)
	if ok {
		return sess, true
	}
	if sess.MustChange {
		writeError(w, http.StatusForbidden, "password change required")
	} else {
		writeError(w, http.StatusUnauthorized, "invalid session")
	}
	return session{}, false
}

func (s *server) requireRoles(w http.ResponseWriter, r *http.Request, roles ...principalRole) (session, bool) {
	sess, ok := s.authorizeSession(w, r)
	if !ok {
		return session{}, false
	}
	for _, role := range roles {
		if sess.Role == role {
			return sess, true
		}
	}
	writeError(w, http.StatusForbidden, "operation not permitted for this role")
	return session{}, false
}

func (s *server) requireAdministrator(w http.ResponseWriter, r *http.Request) (session, bool) {
	return s.requireRoles(w, r, roleAdministrator)
}

func (s *server) requireAdministratorCredentialPath(w http.ResponseWriter, r *http.Request) (session, bool) {
	_, sess, ok := s.authorize(r, true)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid session")
		return session{}, false
	}
	if sess.Role != roleAdministrator {
		writeError(w, http.StatusForbidden, "operation not permitted for this role")
		return session{}, false
	}
	return sess, true
}

func (s *server) requireReadAccess(w http.ResponseWriter, r *http.Request) (session, bool) {
	return s.requireRoles(w, r, roleAdministrator, roleOperator, roleViewer)
}
