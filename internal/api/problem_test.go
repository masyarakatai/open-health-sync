// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type problemBody struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func readProblem(t *testing.T, w *httptest.ResponseRecorder) problemBody {
	t.Helper()
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var p problemBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	return p
}

func TestProblemHelpers(t *testing.T) {
	t.Run("badRequest answers 400 with the detail", func(t *testing.T) {
		w := httptest.NewRecorder()
		badRequest(w, "nope")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		p := readProblem(t, w)
		assert.Equal(t, "Bad Request", p.Title)
		assert.Equal(t, http.StatusBadRequest, p.Status)
		assert.Equal(t, "nope", p.Detail)
	})

	t.Run("notFound names what was asked for", func(t *testing.T) {
		w := httptest.NewRecorder()
		notFound(w, "widget")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "widget not found", readProblem(t, w).Detail)
	})

	t.Run("internalError never sends the cause", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/v1/thing", nil)
		r.Pattern = "GET /v1/thing"
		w := httptest.NewRecorder()
		internalError(w, r, "querying failed", errors.New("password=hunter2"))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		p := readProblem(t, w)
		assert.Equal(t, "Internal Server Error", p.Title)
		assert.Equal(t, "an internal error occurred", p.Detail)
		assert.NotContains(t, w.Body.String(), "hunter2")
	})
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]int{"a": 1})
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"a":1}`, w.Body.String())
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	cases := []struct {
		name       string
		body       string
		allowEmpty bool
		wantOK     bool
		wantStatus int
		wantDetail string
	}{
		{"a valid object", `{"name":"x","value":3}`, false, true, 0, ""},
		{"an empty body is fine when allowed", "", true, true, 0, ""},
		{"an empty body is refused otherwise", "", false, false, http.StatusBadRequest, "unexpected end of input"},
		{"an unknown field", `{"name":"x","bogus":1}`, false, false, http.StatusBadRequest, `unknown field "bogus"`},
		{"a wrong type", `{"value":"three"}`, false, false, http.StatusBadRequest, "field value has the wrong type"},
		{"two JSON values", `{"name":"a"}{"name":"b"}`, false, false, http.StatusBadRequest, "more than one JSON value"},
		{"truncated input", `{"name":`, false, false, http.StatusBadRequest, "unexpected end of input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/x", strings.NewReader(tc.body))
			var v payload
			ok := decodeJSON(w, r, &v, tc.allowEmpty)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				if tc.body != "" {
					assert.Equal(t, "x", v.Name)
					assert.Equal(t, 3, v.Value)
				}
				return
			}
			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Contains(t, readProblem(t, w).Detail, tc.wantDetail)
		})
	}

	t.Run("a body over the cap is 413", func(t *testing.T) {
		w := httptest.NewRecorder()
		over := append([]byte(`{"name":"`), bytes.Repeat([]byte("a"), maxJSONBody)...)
		over = append(over, `"}`...)
		r := httptest.NewRequest("POST", "/v1/x", bytes.NewReader(over))
		var v payload
		require.False(t, decodeJSON(w, r, &v, false))
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
		assert.Contains(t, readProblem(t, w).Detail, "too large")
	})
}

func TestJSONErrDetail(t *testing.T) {
	assert.Equal(t, "unexpected end of input", jsonErrDetail(io.EOF))
	assert.Equal(t, "unexpected end of input", jsonErrDetail(io.ErrUnexpectedEOF))
}
