package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestWriteLiveStatusUpdateEmitsEqualLengthStateChanges(t *testing.T) {
	pendingResources := []store.SessionResource{{SessionID: "session-1", ResourceID: "resource-1", State: "pending"}}
	blockedResources := []store.SessionResource{{SessionID: "session-1", ResourceID: "resource-1", State: "blocked"}}
	pending := liveStatusSnapshot{
		Session:          &store.Session{ID: "session-1"},
		SessionResources: &pendingResources,
	}
	blocked := liveStatusSnapshot{
		Session:          &store.Session{ID: "session-1"},
		SessionResources: &blockedResources,
	}

	pendingPayload, err := json.Marshal(pending)
	require.NoError(t, err)
	blockedPayload, err := json.Marshal(blocked)
	require.NoError(t, err)
	require.Len(t, blockedPayload, len(pendingPayload), "the regression requires distinct payloads with the same length")

	recorder := httptest.NewRecorder()
	var previous []byte
	emitted, err := writeLiveStatusUpdate(recorder, pending, &previous)
	require.NoError(t, err)
	assert.True(t, emitted)
	emitted, err = writeLiveStatusUpdate(recorder, blocked, &previous)
	require.NoError(t, err)
	assert.True(t, emitted)

	bodyAfterChanges := recorder.Body.String()
	assert.Equal(t, 2, strings.Count(bodyAfterChanges, "data: "))
	assert.Contains(t, bodyAfterChanges, `"State":"pending"`)
	assert.Contains(t, bodyAfterChanges, `"State":"blocked"`)

	emitted, err = writeLiveStatusUpdate(recorder, blocked, &previous)
	require.NoError(t, err)
	assert.False(t, emitted)
	assert.Equal(t, bodyAfterChanges, recorder.Body.String())
}

func TestHandleStatusAllowsMissingOptionalLiveState(t *testing.T) {
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	d := &dashboard{store: st, log: zerolog.Nop()}

	recorder := httptest.NewRecorder()
	d.handleStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response statusResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Nil(t, response.Lock)
	assert.Nil(t, response.Session)
}

func TestStatusHandlersReportSnapshotFailures(t *testing.T) {
	st, err := store.New(":memory:")
	require.NoError(t, err)
	require.NoError(t, st.Close())
	d := &dashboard{store: st, log: zerolog.Nop()}

	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{name: "status", handler: d.handleStatus, path: "/api/status"},
		{name: "live status", handler: d.handleLiveStatus, path: "/api/live-status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			assert.Equal(t, http.StatusInternalServerError, recorder.Code)
			assert.JSONEq(t, `{"error":"could not load dashboard status"}`, recorder.Body.String())
		})
	}
}
