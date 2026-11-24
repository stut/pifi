// Package shelltest provides a recording fake for utils.Shell, used by tests
// that need to assert on the sequence of shell commands a module issues.
package shelltest

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Call is a single Shell.Run invocation captured by RecordingShell.
type Call struct {
	Command string
	Timeout time.Duration
}

// Response is what RecordingShell returns for a matched command.
type Response struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// RecordingShell is a utils.Shell implementation that records every call and
// returns scripted responses. Match commands by substring via Expect.
//
// Unmatched commands return ExitCode=0, empty output, no error — tests should
// assert on Calls() to verify what was issued.
type RecordingShell struct {
	mu        sync.Mutex
	calls     []Call
	responses []scriptedResponse
}

type scriptedResponse struct {
	match    string
	response Response
}

// New returns an empty RecordingShell.
func New() *RecordingShell {
	return &RecordingShell{}
}

// Expect registers a scripted response. The first scripted response whose
// match string is contained in the command wins; later expectations for the
// same substring won't fire until earlier matching ones are consumed.
//
// Use Expect to inject failures, exit codes, or stdout for commands the
// module under test will issue.
func (s *RecordingShell) Expect(matchSubstring string, response Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, scriptedResponse{match: matchSubstring, response: response})
}

// Run implements utils.Shell.
func (s *RecordingShell) Run(command string, timeout time.Duration) (int, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, Call{Command: command, Timeout: timeout})

	for i, r := range s.responses {
		if strings.Contains(command, r.match) {
			s.responses = append(s.responses[:i], s.responses[i+1:]...)
			return r.response.ExitCode, r.response.Stdout, r.response.Stderr, r.response.Err
		}
	}
	return 0, "", "", nil
}

// Calls returns a copy of every command issued so far, in order.
func (s *RecordingShell) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// Commands returns just the command strings, for terse assertions.
func (s *RecordingShell) Commands() []string {
	calls := s.Calls()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Command
	}
	return out
}

// Reset clears recorded calls and pending expectations.
func (s *RecordingShell) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
	s.responses = nil
}

// PendingExpectations returns the number of unmatched scripted responses.
// Useful in test teardown to assert all expectations fired.
func (s *RecordingShell) PendingExpectations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.responses)
}

// String returns a debug representation of the recorded calls.
func (s *RecordingShell) String() string {
	calls := s.Calls()
	var b strings.Builder
	for i, c := range calls {
		fmt.Fprintf(&b, "[%d] %s\n", i, c.Command)
	}
	return b.String()
}
