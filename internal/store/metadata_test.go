package store

import (
	"context"
	"testing"
)

func TestMetadata_SetGetRoundTrip(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	m := &SongMetadata{
		CacheKey:      "ttml_lyrics:shape of you ed sheeran",
		VideoIDs:      []string{"vid1"},
		AppleTrackID:  "1234",
		ISRC:          "GBAHS1600463",
		TrackName:     "Shape of You",
		ArtistName:    "Ed Sheeran",
		AlbumName:     "Divide",
		DurationMs:    233000,
		ReleaseDate:   "2017-01-06",
		RawAttributes: `{"genre":"pop"}`,
	}
	if err := testStore.SetSongMetadata(ctx, m); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := testStore.GetSongMetadata(ctx, m.CacheKey)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.TrackName != "Shape of You" || got.ISRC != "GBAHS1600463" || got.AppleTrackID != "1234" {
		t.Errorf("fields mismatch: %+v", got)
	}
	if len(got.VideoIDs) != 1 || got.VideoIDs[0] != "vid1" {
		t.Errorf("videoIds: %v", got.VideoIDs)
	}
	if got.RawAttributes == "" {
		t.Error("raw attributes lost")
	}
}

func TestMetadata_MergePreservesFirstSeenAndUnionsVideoIDs(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	key := "ttml_lyrics:song artist"
	if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: key, TrackName: "Song", ArtistName: "Artist", VideoIDs: []string{"v1"}}); err != nil {
		t.Fatal(err)
	}
	first, _, err := testStore.GetSongMetadata(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	if err := testStore.SetSongMetadata(ctx, &SongMetadata{CacheKey: key, TrackName: "Song", ArtistName: "Artist", VideoIDs: []string{"v2"}}); err != nil {
		t.Fatal(err)
	}
	second, _, err := testStore.GetSongMetadata(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	if !second.FirstSeen.Equal(first.FirstSeen) {
		t.Errorf("first_seen changed: %v -> %v", first.FirstSeen, second.FirstSeen)
	}
	if len(second.VideoIDs) != 2 || second.VideoIDs[0] != "v1" || second.VideoIDs[1] != "v2" {
		t.Errorf("videoIds not unioned: %v", second.VideoIDs)
	}
}

func TestMetadata_ReverseLookups(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	mustSet := func(key, isrc, track, artist string, vids ...string) {
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{
			CacheKey: key, ISRC: isrc, TrackName: track, ArtistName: artist, VideoIDs: vids,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustSet("ttml_lyrics:song artist 180s", "ISRC1", "Song", "Artist", "vidA")
	mustSet("ttml_lyrics:song artist 240s", "ISRC1", "Song", "Artist", "vidB")

	t.Run("by_video_id", func(t *testing.T) {
		keys, err := testStore.GetCacheKeysByVideoID(ctx, "vidA")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0] != "ttml_lyrics:song artist 180s" {
			t.Errorf("keys: %v", keys)
		}
	})

	t.Run("by_isrc", func(t *testing.T) {
		keys, err := testStore.GetCacheKeysByISRC(ctx, "ISRC1")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 2 {
			t.Errorf("expected 2 duration variants, got %v", keys)
		}
	})

	t.Run("by_song_artist_all_variants", func(t *testing.T) {
		keys, err := testStore.GetCacheKeysBySongArtist(ctx, "song", "artist")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 2 {
			t.Errorf("expected 2 variants, got %v", keys)
		}
	})

	t.Run("all_video_ids_for_song", func(t *testing.T) {
		vids, err := testStore.GetAllVideoIDsForSong(ctx, "Song", "Artist")
		if err != nil {
			t.Fatal(err)
		}
		if len(vids) != 2 || vids[0] != "vidA" || vids[1] != "vidB" {
			t.Errorf("videoIds across variants: %v", vids)
		}
	})
}

func TestMetadata_AddVideoIDIndependent(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	key := "ttml_lyrics:x y"
	if err := testStore.AddVideoID(ctx, key, "vidX"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddVideoID(ctx, key, "vidX"); err != nil {
		t.Fatal(err)
	}
	keys, err := testStore.GetCacheKeysByVideoID(ctx, "vidX")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != key {
		t.Errorf("keys: %v", keys)
	}
}
