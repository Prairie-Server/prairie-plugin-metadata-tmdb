package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	pluginv1 "github.com/prairie-server/prairie-plugin-sdk/pkg/pluginproto/prairie/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/prairie-server/prairie-plugin-metadata-tmdb/metadata"
	"github.com/prairie-server/prairie-plugin-metadata-tmdb/models"
	"github.com/prairie-server/prairie-plugin-metadata-tmdb/provider"
	pluginsdkruntime "github.com/prairie-server/prairie-plugin-sdk/pkg/pluginsdk/runtime"
)

func TestRuntimeServerConfigure_NoOp(t *testing.T) {
	server := &runtimeServer{provider: provider.NewProvider()}

	_, err := server.Configure(context.Background(), &pluginv1.ConfigureRequest{})
	if err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}

	p, err := server.providerForRequest()
	if err != nil {
		t.Fatalf("providerForRequest() returned error: %v", err)
	}
	if p == nil {
		t.Fatal("expected provider to be available")
	}
}

func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()

	result, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatalf("structpb.NewStruct() returned error: %v", err)
	}
	return result
}

func metadataItemReleaseDate(t *testing.T, item *pluginv1.MetadataItem) string {
	t.Helper()

	field := item.ProtoReflect().Descriptor().Fields().ByName("release_date")
	if field == nil {
		t.Fatal("MetadataItem descriptor is missing release_date")
	}

	value := item.ProtoReflect().Get(field)
	if value.Interface() == nil {
		return ""
	}
	return value.String()
}

func TestResolveImageURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		variant string
		wantURL string
	}{
		// poster variants
		{name: "poster card", path: "tmdb://poster/poster.jpg", variant: "card", wantURL: "https://image.tmdb.org/t/p/w300/poster.jpg"},
		{name: "poster featured", path: "tmdb://poster/poster.jpg", variant: "featured", wantURL: "https://image.tmdb.org/t/p/w500/poster.jpg"},
		{name: "poster full", path: "tmdb://poster/poster.jpg", variant: "full", wantURL: "https://image.tmdb.org/t/p/w780/poster.jpg"},
		{name: "poster original", path: "tmdb://poster/poster.jpg", variant: "original", wantURL: "https://image.tmdb.org/t/p/original/poster.jpg"},
		{name: "poster empty variant", path: "tmdb://poster/poster.jpg", variant: "", wantURL: "https://image.tmdb.org/t/p/original/poster.jpg"},
		// backdrop variants
		{name: "backdrop featured", path: "tmdb://backdrop/backdrop.jpg", variant: "featured", wantURL: "https://image.tmdb.org/t/p/w1280/backdrop.jpg"},
		{name: "backdrop card", path: "tmdb://backdrop/backdrop.jpg", variant: "card", wantURL: "https://image.tmdb.org/t/p/w300/backdrop.jpg"},
		// still variants
		{name: "still card", path: "tmdb://still/still.jpg", variant: "card", wantURL: "https://image.tmdb.org/t/p/w300/still.jpg"},
		// logo variants
		{name: "logo featured", path: "tmdb://logo/logo.png", variant: "featured", wantURL: "https://image.tmdb.org/t/p/w500/logo.png"},
		// profile variants
		{name: "profile card", path: "tmdb://profile/person.jpg", variant: "card", wantURL: "https://image.tmdb.org/t/p/w185/person.jpg"},
		// empty path
		{name: "empty path", path: "", variant: "card", wantURL: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each sub-test gets its own mock server to avoid shared-state issues
			// with the client's sync.Once configuration cache.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/configuration" {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"images": map[string]any{
							"secure_base_url": "https://image.tmdb.org/t/p/",
						},
					})
					return
				}
				t.Errorf("unexpected path: %s", r.URL.Path)
				http.NotFound(w, r)
			}))
			t.Cleanup(server.Close)

			client := provider.NewClient(1000)
			client.SetBaseURL(server.URL)
			p := provider.NewProviderWithClient(client)

			rs := &runtimeServer{provider: p}
			ms := &metadataServer{runtime: rs}

			resp, err := ms.ResolveImageURL(context.Background(), &pluginv1.ResolveImageURLRequest{
				Path:    tc.path,
				Variant: tc.variant,
			})
			if err != nil {
				t.Fatalf("ResolveImageURL() error = %v", err)
			}
			if resp.GetUrl() != tc.wantURL {
				t.Fatalf("URL = %q, want %q", resp.GetUrl(), tc.wantURL)
			}
		})
	}
}

func TestResolveImageURL_RetriesConfigurationAfterCanceledContext(t *testing.T) {
	t.Parallel()

	configCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/configuration" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		configCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"images": map[string]any{
				"secure_base_url": "https://image.tmdb.org/t/p/",
			},
		})
	}))
	t.Cleanup(server.Close)

	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)

	ms := &metadataServer{
		runtime: &runtimeServer{
			provider: provider.NewProviderWithClient(client),
		},
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ms.ResolveImageURL(canceledCtx, &pluginv1.ResolveImageURLRequest{
		Path:    "tmdb://poster/poster.jpg",
		Variant: "featured",
	}); err == nil {
		t.Fatal("ResolveImageURL() with canceled context succeeded, want error")
	}

	resp, err := ms.ResolveImageURL(context.Background(), &pluginv1.ResolveImageURLRequest{
		Path:    "tmdb://poster/poster.jpg",
		Variant: "featured",
	})
	if err != nil {
		t.Fatalf("ResolveImageURL() after canceled context error = %v", err)
	}
	if got, want := resp.GetUrl(), "https://image.tmdb.org/t/p/w500/poster.jpg"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if configCalls != 1 {
		t.Fatalf("configuration calls = %d, want 1", configCalls)
	}
}

func TestMetadataServerGetMetadata_IncludesReleaseDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": map[string]any{
					"secure_base_url": "https://image.tmdb.org/t/p/",
				},
			})
		case "/movie/123":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                123,
				"title":             "Example Movie",
				"original_title":    "Example Movie",
				"overview":          "Overview",
				"tagline":           "Tagline",
				"release_date":      "2024-01-02",
				"runtime":           120,
				"vote_average":      7.2,
				"original_language": "en",
				"genres":            []map[string]any{{"id": 1, "name": "Drama"}},
				"production_companies": []map[string]any{
					{"id": 2, "name": "Studio"},
				},
				"origin_country": []string{"US"},
				"external_ids": map[string]any{
					"imdb_id": "tt1234567",
				},
				"credits": map[string]any{
					"cast": []any{},
					"crew": []any{},
				},
				"release_dates": map[string]any{
					"results": []any{},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)

	ms := &metadataServer{
		runtime: &runtimeServer{
			provider: provider.NewProviderWithClient(client),
		},
	}

	resp, err := ms.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{
		ProviderId: "123",
		ItemType:   "movie",
	})
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}

	if got := metadataItemReleaseDate(t, resp.GetItem()); got != "2024-01-02" {
		t.Fatalf("release_date = %q, want 2024-01-02", got)
	}
}

func TestMetadataRequestFromProto_CarriesContextFields(t *testing.T) {
	req := &pluginv1.GetMetadataRequest{
		ProviderId: "123",
		ItemType:   "movie",
		ProviderIds: mustStruct(t, map[string]any{
			"imdb": "tt1234567",
		}),
		Language: "fr",
		FilePath: "/media/movies/example.mkv",
	}

	got := metadataRequestFromProto(req, "tmdb")

	if got.ContentType != "movie" {
		t.Fatalf("ContentType = %q, want movie", got.ContentType)
	}
	if got.Language != "fr" {
		t.Fatalf("Language = %q, want fr", got.Language)
	}
	if got.FilePath != "/media/movies/example.mkv" {
		t.Fatalf("FilePath = %q, want /media/movies/example.mkv", got.FilePath)
	}

	wantIDs := map[string]string{
		"tmdb": "123",
		"imdb": "tt1234567",
	}
	if !reflect.DeepEqual(got.ProviderIDs, wantIDs) {
		t.Fatalf("ProviderIDs = %#v, want %#v", got.ProviderIDs, wantIDs)
	}
}

func TestAssetRequestsFromProto_CarryProviderContext(t *testing.T) {
	imageReq := imageRequestFromProto(&pluginv1.GetImagesRequest{
		ProviderId: "123",
		ItemType:   "movie",
		ProviderIds: mustStruct(t, map[string]any{
			"imdb": "tt1234567",
		}),
		Language: "es",
	}, "tmdb")
	if imageReq.ContentType != "movie" {
		t.Fatalf("image ContentType = %q, want movie", imageReq.ContentType)
	}
	if imageReq.Language != "es" {
		t.Fatalf("image Language = %q, want es", imageReq.Language)
	}
	if !reflect.DeepEqual(imageReq.ProviderIDs, map[string]string{
		"tmdb": "123",
		"imdb": "tt1234567",
	}) {
		t.Fatalf("image ProviderIDs = %#v", imageReq.ProviderIDs)
	}

	seasonsReq := seasonsRequestFromProto(&pluginv1.GetSeasonsRequest{
		SeriesProviderId: "series-1",
		ProviderIds: mustStruct(t, map[string]any{
			"tvdb": "81189",
		}),
	}, "tmdb")
	if seasonsReq.ContentType != "series" {
		t.Fatalf("seasons ContentType = %q, want series", seasonsReq.ContentType)
	}
	if !reflect.DeepEqual(seasonsReq.ProviderIDs, map[string]string{
		"tmdb": "series-1",
		"tvdb": "81189",
	}) {
		t.Fatalf("seasons ProviderIDs = %#v", seasonsReq.ProviderIDs)
	}

	episodesReq := episodesRequestFromProto(&pluginv1.GetEpisodesRequest{
		SeriesProviderId: "series-1",
		SeasonNumber:     2,
		ProviderIds: mustStruct(t, map[string]any{
			"tvdb": "81189",
		}),
	}, "tmdb")
	if episodesReq.SeasonNumber != 2 {
		t.Fatalf("SeasonNumber = %d, want 2", episodesReq.SeasonNumber)
	}
	if !reflect.DeepEqual(episodesReq.ProviderIDs, map[string]string{
		"tmdb": "series-1",
		"tvdb": "81189",
	}) {
		t.Fatalf("episodes ProviderIDs = %#v", episodesReq.ProviderIDs)
	}
}

func TestMetadataServerGetPersonDetail_CanonicalizesProfilePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/person/287":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             287,
				"name":           "Brad Pitt",
				"biography":      "Biography",
				"birthday":       "1963-12-18",
				"place_of_birth": "Shawnee, Oklahoma, USA",
				"profile_path":   "/brad.jpg",
				"external_ids": map[string]any{
					"imdb_id": "nm0000093",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)

	ms := &metadataServer{
		runtime: &runtimeServer{
			provider: provider.NewProviderWithClient(client),
		},
	}

	resp, err := ms.GetPersonDetail(context.Background(), &pluginv1.GetPersonDetailRequest{
		ProviderIds: mustStruct(t, map[string]any{
			"tmdb": "287",
		}),
	})
	if err != nil {
		t.Fatalf("GetPersonDetail() error = %v", err)
	}
	if resp.GetPerson() == nil {
		t.Fatal("expected person detail record")
	}
	if resp.GetPerson().GetPhotoPath() != "tmdb://profile/brad.jpg" {
		t.Fatalf("PhotoPath = %q, want tmdb://profile/brad.jpg", resp.GetPerson().GetPhotoPath())
	}
	if resp.GetPerson().GetProviderIds().AsMap()["imdb"] != "nm0000093" {
		t.Fatalf("provider_ids[imdb] = %v, want nm0000093", resp.GetPerson().GetProviderIds().AsMap()["imdb"])
	}
}

func TestRuntimeServerGetManifest(t *testing.T) {
	manifest, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	rs := &runtimeServer{manifest: manifest, provider: provider.NewProvider()}
	resp, err := rs.GetManifest(context.Background(), &pluginv1.GetManifestRequest{})
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if resp.GetManifest() != manifest {
		t.Fatal("manifest pointer mismatch")
	}
}

func TestLoadManifest(t *testing.T) {
	original := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = original })

	manifest, err := loadManifest()
	if err != nil {
		t.Fatalf("loadManifest: %v", err)
	}
	if manifest.GetPluginId() != "prairie.tmdb" {
		t.Fatalf("PluginId = %q", manifest.GetPluginId())
	}
	if manifest.GetVersion() != "9.9.9-test" {
		t.Fatalf("Version = %q", manifest.GetVersion())
	}
	if len(manifest.GetChecksum()) != 64 {
		t.Fatalf("Checksum length = %d", len(manifest.GetChecksum()))
	}
}

func TestLoadManifestErrorPaths(t *testing.T) {
	originalManifest := manifestJSON
	originalExecutable := osExecutable
	originalReadFile := osReadFile
	t.Cleanup(func() {
		manifestJSON = originalManifest
		osExecutable = originalExecutable
		osReadFile = originalReadFile
	})

	manifestJSON = []byte(`{`)
	if _, err := loadManifest(); err == nil {
		t.Fatal("expected invalid manifest error")
	}
	manifestJSON = originalManifest

	osExecutable = func() (string, error) {
		return "", errors.New("no executable")
	}
	if _, err := loadManifest(); err == nil {
		t.Fatal("expected executable error")
	}
	osExecutable = originalExecutable

	osReadFile = func(string) ([]byte, error) {
		return nil, errors.New("read failed")
	}
	if _, err := loadManifest(); err == nil {
		t.Fatal("expected read executable error")
	}
}

func TestMainWiresRuntimeServers(t *testing.T) {
	originalServe := runtimeServe
	t.Cleanup(func() { runtimeServe = originalServe })

	called := false
	runtimeServe = func(cfg pluginsdkruntime.ServeConfig) {
		called = true
		if cfg.Servers.Runtime == nil || cfg.Servers.MetadataProvider == nil || cfg.Servers.ImageResolver == nil {
			t.Fatalf("missing runtime servers: %#v", cfg.Servers)
		}
	}

	main()
	if !called {
		t.Fatal("runtimeServe was not called")
	}
}

func TestMetadataServerSearchSeasonsEpisodesImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"images": map[string]any{"secure_base_url": "https://image.tmdb.org/t/p/"}})
		case "/search/movie":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
				"id": 42, "title": "Ten", "original_title": "Dieci", "original_language": "it",
				"release_date": "2022-01-01", "poster_path": "/p.jpg", "overview": "ov",
			}}})
		case "/tv/77":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 77, "name": "Show", "original_name": "Show", "original_language": "en",
				"status": "Ended", "overview": "ov", "genres": []any{}, "networks": []any{},
				"credits":      map[string]any{"cast": []any{}, "crew": []any{}},
				"external_ids": map[string]any{}, "images": map[string]any{
					"posters":   []map[string]any{{"file_path": "/poster.jpg", "width": 1, "height": 2, "vote_average": 5}},
					"backdrops": []map[string]any{{"file_path": "/bd.jpg", "width": 3, "height": 4, "vote_average": 4}},
					"logos":     []map[string]any{{"file_path": "/logo.png", "width": 5, "height": 6, "vote_average": 3}},
				},
				"content_ratings": map[string]any{"results": []any{}},
				"seasons": []map[string]any{{
					"season_number": 1, "name": "S1", "overview": "so", "air_date": "2020-01-01", "poster_path": "/s.jpg",
				}},
			})
		case "/tv/77/season/1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 1,
				"episodes": []map[string]any{{
					"id": 9001, "season_number": 1, "episode_number": 1, "name": "Pilot",
					"overview": "ep", "air_date": "2020-01-02", "runtime": 42, "still_path": "/still.jpg",
					"vote_average": 7.5,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)
	ms := &metadataServer{runtime: &runtimeServer{provider: provider.NewProviderWithClient(client)}}

	search, err := ms.Search(context.Background(), &pluginv1.SearchMetadataRequest{
		Query: "Ten", Year: 2022, ItemType: "movie", Language: "en",
	})
	if err != nil || len(search.GetResults()) != 1 {
		t.Fatalf("Search = %#v err=%v", search, err)
	}
	if search.GetResults()[0].GetImageUrl() != "tmdb://poster/p.jpg" {
		t.Fatalf("ImageUrl = %q", search.GetResults()[0].GetImageUrl())
	}

	seasons, err := ms.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{
		SeriesProviderId: "77", Language: "en",
	})
	if err != nil || len(seasons.GetSeasons()) != 1 {
		t.Fatalf("GetSeasons = %#v err=%v", seasons, err)
	}
	if seasons.GetSeasons()[0].GetPosterPath() != "tmdb://poster/s.jpg" {
		t.Fatalf("season poster = %q", seasons.GetSeasons()[0].GetPosterPath())
	}

	episodes, err := ms.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{
		SeriesProviderId: "77", SeasonNumber: 1, Language: "en",
	})
	if err != nil || len(episodes.GetEpisodes()) != 1 {
		t.Fatalf("GetEpisodes = %#v err=%v", episodes, err)
	}
	if episodes.GetEpisodes()[0].GetStillPath() != "tmdb://still/still.jpg" {
		t.Fatalf("still = %q", episodes.GetEpisodes()[0].GetStillPath())
	}

	images, err := ms.GetImages(context.Background(), &pluginv1.GetImagesRequest{
		ProviderId: "77", ItemType: "series", Language: "en",
	})
	if err != nil || len(images.GetImages()) < 3 {
		t.Fatalf("GetImages = %#v err=%v", images, err)
	}
	foundRating := false
	for _, img := range images.GetImages() {
		if img.GetMetadata() != nil {
			foundRating = true
		}
	}
	if !foundRating {
		t.Fatal("expected image rating metadata")
	}

	urls, err := ms.ResolveImageURLs(context.Background(), &pluginv1.ResolveImageURLsRequest{
		Paths:   []string{"tmdb://poster/p.jpg", "tmdb://backdrop/b.jpg", "", "bad"},
		Variant: "card",
	})
	if err != nil {
		t.Fatalf("ResolveImageURLs: %v", err)
	}
	if urls.GetUrls()["tmdb://poster/p.jpg"] != "https://image.tmdb.org/t/p/w300/p.jpg" {
		t.Fatalf("urls = %#v", urls.GetUrls())
	}
}

func TestHelpersAliasesPeopleRatingsMetadataStruct(t *testing.T) {
	aliases := aliasesToProto([]metadata.TitleAlias{
		{Title: " ", Language: "en", Kind: "alternate"},
		{Title: "Alt", Language: "en", Kind: "alternate"},
	})
	if len(aliases) != 1 {
		t.Fatalf("aliases = %#v", aliases)
	}

	people := peopleToRecords(nil)
	if people != nil {
		t.Fatalf("nil people = %#v", people)
	}
	people = peopleToRecords([]models.ItemPerson{{
		Person: models.Person{Name: "A", TmdbID: "1", PhotoPath: "/a.jpg"},
		Kind:   models.PersonKindActor, Character: "Hero", SortOrder: 1,
	}})
	if len(people) != 1 || people[0].GetPhotoPath() != "tmdb://profile/a.jpg" {
		t.Fatalf("people = %#v", people)
	}

	if empty := ratingsMap(metadata.Ratings{}); len(empty) != 0 {
		t.Fatalf("empty ratingsMap = %#v", empty)
	}
	m := ratingsMap(metadata.Ratings{IMDB: 1, TMDB: 2, RTCritic: 3, RTAudience: 4})
	if len(m) != 4 {
		t.Fatalf("ratingsMap = %#v", m)
	}
	if ratingsStruct(metadata.Ratings{TMDB: 1}) == nil {
		t.Fatal("ratingsStruct nil")
	}
	if metadataStruct(&metadata.MetadataResult{}) != nil {
		t.Fatal("empty metadataStruct should be nil")
	}
	ms := metadataStruct(&metadata.MetadataResult{Keywords: []string{" a ", "", "b"}})
	if ms == nil || len(ms.AsMap()["keywords"].([]any)) != 2 {
		t.Fatalf("metadataStruct = %#v", ms)
	}
	if s, err := stringStruct(nil); err != nil || s != nil {
		t.Fatalf("stringStruct nil = %v %v", s, err)
	}
	if s, err := stringStruct(map[string]string{"a": ""}); err != nil || s != nil {
		t.Fatalf("stringStruct empty values = %v %v", s, err)
	}
	if structFromMap(nil) != nil {
		t.Fatal("structFromMap nil")
	}
	if tmdbVariantSize("featured", "profile") != "w185" {
		t.Fatal("featured profile size")
	}
	if tmdbVariantSize("full", "backdrop") != "original" {
		t.Fatal("full backdrop size")
	}
	if resolveOneTMDBPath(provider.NewProvider(), "noslash", "card") != "" {
		t.Fatal("resolveOneTMDBPath should reject bare role")
	}
}

func TestGetPersonDetailNilResult(t *testing.T) {
	ms := &metadataServer{runtime: &runtimeServer{provider: provider.NewProvider()}}
	resp, err := ms.GetPersonDetail(context.Background(), &pluginv1.GetPersonDetailRequest{})
	if err != nil || resp.GetPerson() != nil {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
}

func TestGetMetadataNilResult(t *testing.T) {
	ms := &metadataServer{runtime: &runtimeServer{provider: provider.NewProvider()}}
	resp, err := ms.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{
		ProviderId: "", ItemType: "movie",
	})
	if err != nil {
		t.Fatalf("GetMetadata err=%v", err)
	}
	if resp != nil && resp.GetItem() != nil {
		t.Fatalf("expected nil item, got %#v", resp.GetItem())
	}
}

func TestStructFromMapPanicsOnInvalidValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = structFromMap(map[string]any{"bad": make(chan int)})
}

func TestResolveImageURLConfigurationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)
	ms := &metadataServer{runtime: &runtimeServer{provider: provider.NewProviderWithClient(client)}}
	if _, err := ms.ResolveImageURL(context.Background(), &pluginv1.ResolveImageURLRequest{Path: "tmdb://poster/x.jpg"}); err == nil {
		t.Fatal("expected config error")
	}
	if _, err := ms.ResolveImageURLs(context.Background(), &pluginv1.ResolveImageURLsRequest{Paths: []string{"tmdb://poster/x.jpg"}}); err == nil {
		t.Fatal("expected config error")
	}
	if _, err := ms.Search(context.Background(), &pluginv1.SearchMetadataRequest{Query: "x", ItemType: "movie"}); err == nil {
		t.Fatal("expected search config error")
	}
}

func TestMetadataServerPropagatesProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/configuration" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"images": map[string]any{"secure_base_url": "https://image.tmdb.org/t/p/"},
			})
			return
		}
		http.Error(w, "provider failed", http.StatusBadRequest)
	}))
	defer server.Close()

	client := provider.NewClient(1000)
	client.SetBaseURL(server.URL)
	ms := &metadataServer{runtime: &runtimeServer{provider: provider.NewProviderWithClient(client)}}

	if _, err := ms.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{ProviderId: "1", ItemType: "movie"}); err == nil {
		t.Fatal("expected metadata error")
	}
	if _, err := ms.GetPersonDetail(context.Background(), &pluginv1.GetPersonDetailRequest{
		ProviderIds: mustStruct(t, map[string]any{"tmdb": "1"}),
	}); err == nil {
		t.Fatal("expected person detail error")
	}
	if _, err := ms.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{SeriesProviderId: "1"}); err == nil {
		t.Fatal("expected seasons error")
	}
	if _, err := ms.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{SeriesProviderId: "1", SeasonNumber: 1}); err == nil {
		t.Fatal("expected episodes error")
	}
	if _, err := ms.GetImages(context.Background(), &pluginv1.GetImagesRequest{ProviderId: "1", ItemType: "movie"}); err == nil {
		t.Fatal("expected images error")
	}
}

func TestRecordConvertersRejectInvalidProviderIDKeys(t *testing.T) {
	badIDs := map[string]string{string([]byte{0xff}): "bad"}
	if _, err := metadataItemFromResult(&metadata.MetadataResult{ProviderIDs: badIDs}, "movie"); err == nil {
		t.Fatal("expected metadata item conversion error")
	}
	if _, err := personDetailRecordFromResult(&metadata.PersonDetailResult{ProviderIDs: badIDs}); err == nil {
		t.Fatal("expected person detail conversion error")
	}
}
