package httpserver

import "net/http"

// moduleRegistrar lets feature files register routes without editing the
// central route table. Each feature file may define:
//
//	func init() { registerModule(registerProfile) }
//
// and registerProfile then attaches its handlers to the mux. This keeps
// parallel feature work from conflicting in routes.go.
type moduleRegistrar func(*App, *http.ServeMux)

var moduleRegistrars []moduleRegistrar

func registerModule(fn moduleRegistrar) {
	if fn != nil {
		moduleRegistrars = append(moduleRegistrars, fn)
	}
}
