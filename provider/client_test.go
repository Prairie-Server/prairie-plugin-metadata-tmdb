package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientImageURL(t *testing.T) {
	t.Parallel()
	c := NewClient(1000)
	c.imageBase = "https://image.tmdb.org/t/p/"
	if got := c.ImageURL("", "w500"); got != "" {
		t.Fatalf("empty path = %q", got)
	}
	if got := c.ImageURL("/poster.jpg", "w500"); got != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("ImageURL = %q", got)
	}
}

func TestClientDoGetErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("4xx with api message", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_message": "The resource you requested could not be found.",
			})
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		err := c.doGet(context.Background(), "/missing", &dest)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("4xx without message", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("not-json"))
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/bad", &dest); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("decode error", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{"))
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/bad-json", &dest); err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("429 then success", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		start := time.Now()
		if err := c.doGet(context.Background(), "/retry", &dest); err != nil {
			t.Fatalf("doGet error = %v", err)
		}
		if time.Since(start) < time.Second {
			t.Fatal("expected Retry-After backoff")
		}
	})

	t.Run("429 canceled during wait", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var dest map[string]any
		if err := c.doGet(ctx, "/rate", &dest); err == nil {
			t.Fatal("expected canceled error")
		}
	})

	t.Run("5xx canceled during wait", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var dest map[string]any
		if err := c.doGet(ctx, "/500", &dest); err == nil {
			t.Fatal("expected canceled error")
		}
	})

	t.Run("path already has query", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("api_key") == "" {
				t.Fatal("missing api_key")
			}
			if r.URL.Query().Get("page") != "2" {
				t.Fatalf("page = %q", r.URL.Query().Get("page"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/x?page=2", &dest); err != nil {
			t.Fatalf("doGet error = %v", err)
		}
	})

	t.Run("invalid request url", func(t *testing.T) {
		t.Parallel()
		c := NewClient(1000)
		c.SetBaseURL("http://example.com/%zz")
		var dest map[string]any
		if err := c.doGet(context.Background(), "", &dest); err == nil {
			t.Fatal("expected request creation error")
		}
	})

	t.Run("request failure", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := server.URL
		server.Close()
		c := NewClient(1000)
		c.httpClient.Timeout = 50 * time.Millisecond
		c.SetBaseURL(url)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/down", &dest); err == nil {
			t.Fatal("expected request failure")
		}
	})
}

func TestClientEndpointErrorReturns(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	c := NewClient(1000)
	c.SetBaseURL(server.URL)

	if _, err := c.SearchMovie(context.Background(), "x", 0, "en-US"); err == nil {
		t.Fatal("expected SearchMovie error")
	}
	if _, err := c.SearchTV(context.Background(), "x", 0, "en-US"); err == nil {
		t.Fatal("expected SearchTV error")
	}
	if _, err := c.GetMovie(context.Background(), 1, "en-US"); err == nil {
		t.Fatal("expected GetMovie error")
	}
	if _, err := c.GetTV(context.Background(), 1, "en-US"); err == nil {
		t.Fatal("expected GetTV error")
	}
	if _, err := c.GetPerson(context.Background(), 1, "en-US"); err == nil {
		t.Fatal("expected GetPerson error")
	}
	if _, err := c.GetSeason(context.Background(), 1, 1, "en-US"); err == nil {
		t.Fatal("expected GetSeason error")
	}
}

func TestClientCollectionAndTrendingErrorBranches(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	c := NewClient(1000)
	c.SetBaseURL(server.URL)

	if _, err := c.GetTrending(context.Background(), "movie", "day", 5); err == nil {
		t.Fatal("expected trending error")
	}
	if _, err := c.GetCollectionPreset(context.Background(), "trending", "movie", "day", 5); err == nil {
		t.Fatal("expected trending preset error")
	}
	if _, err := c.GetCollectionPreset(context.Background(), "top_rated", "tv", "", 5); err == nil {
		t.Fatal("expected tv preset error")
	}
	if _, err := c.GetCollectionPreset(context.Background(), "upcoming", "movie", "", 5); err == nil {
		t.Fatal("expected movie preset error")
	}
	if _, err := c.GetCollectionPreset(context.Background(), "on_the_air", "tv", "", 5); err == nil {
		t.Fatal("expected on_the_air preset error")
	}
}

func TestClientCollectionPresetOversizedFirstPageIsTrimmed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"page":        1,
			"total_pages": 1,
			"results": []map[string]any{
				{"id": 1, "title": "A"},
				{"id": 2, "title": "B"},
				{"id": 3, "title": "C"},
			},
		})
	}))
	t.Cleanup(server.Close)

	c := NewClient(1000)
	c.SetBaseURL(server.URL)
	results, err := c.GetCollectionPreset(context.Background(), "popular", "movie", "", 2)
	if err != nil {
		t.Fatalf("GetCollectionPreset error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
}

func TestClientGetTrending(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/trending/movie/day":
			page := r.URL.Query().Get("page")
			if page == "1" || page == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"page": 1, "total_pages": 2,
					"results": []map[string]any{
						{"id": 1, "title": "One", "media_type": "movie"},
						{"id": 2, "title": "Two", "media_type": "movie"},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 2, "total_pages": 2,
				"results": []map[string]any{
					{"id": 3, "title": "Three", "media_type": "movie"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	c := NewClient(1000)
	c.SetBaseURL(server.URL)

	if _, err := c.GetTrending(context.Background(), "invalid", "day", 10); err == nil {
		t.Fatal("expected invalid media type error")
	}
	if _, err := c.GetTrending(context.Background(), "movie", "year", 10); err == nil {
		t.Fatal("expected invalid time window error")
	}

	results, err := c.GetTrending(context.Background(), "movie", "day", 0)
	if err != nil {
		t.Fatalf("GetTrending error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}

	limited, err := c.GetTrending(context.Background(), "movie", "day", 2)
	if err != nil {
		t.Fatalf("GetTrending limited error = %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len(limited) = %d, want 2", len(limited))
	}

	capped, err := c.GetTrending(context.Background(), "movie", "day", 1000)
	if err != nil {
		t.Fatalf("GetTrending capped error = %v", err)
	}
	if len(capped) != 3 {
		t.Fatalf("len(capped) = %d, want 3", len(capped))
	}
}

func TestClientGetCollectionPreset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/trending/all/week":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{
					{"id": 1, "title": "Movie Title", "media_type": "movie"},
					{"id": 2, "name": "TV Name", "media_type": "tv"},
				},
			})
		case "/movie/popular":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{{"id": 10, "title": "Popular Movie"}},
			})
		case "/tv/top_rated":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{{"id": 20, "name": "Top TV"}},
			})
		case "/movie/now_playing":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{{"id": 30, "title": "Now Playing"}},
			})
		case "/tv/airing_today":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{{"id": 40, "name": "Airing"}},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	c := NewClient(1000)
	c.SetBaseURL(server.URL)

	if _, err := c.GetCollectionPreset(context.Background(), "nope", "movie", "", 5); err == nil {
		t.Fatal("expected invalid preset")
	}

	trending, err := c.GetCollectionPreset(context.Background(), "trending", "all", "week", 0)
	if err != nil {
		t.Fatalf("trending error = %v", err)
	}
	if len(trending) != 2 || trending[0].Title != "Movie Title" || trending[1].Title != "TV Name" {
		t.Fatalf("trending = %#v", trending)
	}

	popular, err := c.GetCollectionPreset(context.Background(), "popular", "movie", "", 5)
	if err != nil {
		t.Fatalf("popular error = %v", err)
	}
	if len(popular) != 1 || popular[0].MediaType != "movie" {
		t.Fatalf("popular = %#v", popular)
	}

	tvTop, err := c.GetCollectionPreset(context.Background(), "top_rated", "tv", "", 5)
	if err != nil {
		t.Fatalf("tv top error = %v", err)
	}
	if len(tvTop) != 1 || tvTop[0].Title != "Top TV" {
		t.Fatalf("tvTop = %#v", tvTop)
	}

	nowPlaying, err := c.GetCollectionPreset(context.Background(), "now_playing", "movie", "", 5)
	if err != nil {
		t.Fatalf("now_playing error = %v", err)
	}
	if len(nowPlaying) != 1 {
		t.Fatalf("nowPlaying = %#v", nowPlaying)
	}

	airing, err := c.GetCollectionPreset(context.Background(), "airing_today", "tv", "", 5)
	if err != nil {
		t.Fatalf("airing_today error = %v", err)
	}
	if len(airing) != 1 {
		t.Fatalf("airing = %#v", airing)
	}

	if _, _, _, _, err := normalizeCollectionPreset("trending", "person", "day"); err == nil {
		t.Fatal("expected trending media type error")
	}
	if _, _, _, _, err := normalizeCollectionPreset("trending", "movie", "month"); err == nil {
		t.Fatal("expected trending window error")
	}
	if _, _, _, _, err := normalizeCollectionPreset("popular", "all", ""); err == nil {
		t.Fatal("expected popular media type error")
	}
	if _, _, _, _, err := normalizeCollectionPreset("now_playing", "tv", ""); err == nil {
		t.Fatal("expected now_playing media type error")
	}
	if _, _, _, _, err := normalizeCollectionPreset("on_the_air", "movie", ""); err == nil {
		t.Fatal("expected on_the_air media type error")
	}
	preset, media, window, path, err := normalizeCollectionPreset("upcoming", "movie", "")
	if err != nil || preset != "upcoming" || media != "movie" || window != "" || path != "/movie/upcoming" {
		t.Fatalf("upcoming normalize = %q %q %q %q %v", preset, media, window, path, err)
	}
	_, _, _, path, err = normalizeCollectionPreset("on_the_air", "tv", "")
	if err != nil || path != "/tv/on_the_air" {
		t.Fatalf("on_the_air path = %q err=%v", path, err)
	}
}

func TestClientSearchTV(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("first_air_date_year") != "2001" {
			t.Fatalf("year = %q", r.URL.Query().Get("first_air_date_year"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{"id": 77, "name": "Series"}},
		})
	}))
	t.Cleanup(server.Close)
	c := NewClient(1000)
	c.SetBaseURL(server.URL)
	results, err := c.SearchTV(context.Background(), "Series", 2001, "en-US")
	if err != nil {
		t.Fatalf("SearchTV error = %v", err)
	}
	if len(results) != 1 || results[0].ID != 77 {
		t.Fatalf("results = %#v", results)
	}
}

func TestClientRetryAfterInvalidHeaderAnd5xxSuccess(t *testing.T) {
	t.Parallel()

	t.Run("invalid retry-after", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "nope")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/r", &dest); err != nil {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("5xx then success", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		t.Cleanup(server.Close)
		c := NewClient(1000)
		c.SetBaseURL(server.URL)
		var dest map[string]any
		if err := c.doGet(context.Background(), "/5", &dest); err != nil {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestClientGetCollectionPresetPaginationAndUpcoming(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/movie/upcoming":
			page := r.URL.Query().Get("page")
			if page == "1" || page == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"page": 1, "total_pages": 2,
					"results": []map[string]any{{"id": 1, "title": "A"}, {"id": 2, "title": "B"}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 2, "total_pages": 2,
				"results": []map[string]any{{"id": 3, "title": "C"}},
			})
		case "/tv/on_the_air":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "total_pages": 1,
				"results": []map[string]any{{"id": 9, "name": "OnAir"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := NewClient(1000)
	c.SetBaseURL(server.URL)
	all, err := c.GetCollectionPreset(context.Background(), "upcoming", "movie", "", 1000)
	if err != nil || len(all) != 3 {
		t.Fatalf("upcoming all = %#v err=%v", all, err)
	}
	limited, err := c.GetCollectionPreset(context.Background(), "upcoming", "movie", "", 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("upcoming limited = %#v err=%v", limited, err)
	}
	onAir, err := c.GetCollectionPreset(context.Background(), "on_the_air", "tv", "", 5)
	if err != nil || len(onAir) != 1 {
		t.Fatalf("on_the_air = %#v err=%v", onAir, err)
	}
}

func TestClientLoadConfigurationCached(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"images": map[string]any{"secure_base_url": "https://img/"}})
	}))
	t.Cleanup(server.Close)
	c := NewClient(1000)
	c.SetBaseURL(server.URL)
	if err := c.loadConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.loadConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestClientDoGetCanceledBeforeRequest(t *testing.T) {
	t.Parallel()
	c := NewClient(1000)
	c.SetBaseURL("http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var dest map[string]any
	if err := c.doGet(ctx, "/x", &dest); err == nil {
		t.Fatal("expected canceled")
	}
}

func TestClientSearchMovieWithoutYear(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("year") != "" {
			t.Fatalf("unexpected year %q", r.URL.Query().Get("year"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	t.Cleanup(server.Close)
	c := NewClient(1000)
	c.SetBaseURL(server.URL)
	if _, err := c.SearchMovie(context.Background(), "x", 0, "en-US"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchTV(context.Background(), "x", 0, "en-US"); err != nil {
		t.Fatal(err)
	}
}
