package httpapi

import (
	"context"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

func (s *Server) getSongMetadata(ctx context.Context, cacheKey string) (*store.SongMetadata, bool) {
	m, ok, err := s.store.GetSongMetadata(ctx, cacheKey)
	if err != nil {
		log.Errorf("%s Error reading metadata: %v", logcolors.LogCache, err)
		return nil, false
	}
	return m, ok
}

func (s *Server) setSongMetadata(ctx context.Context, meta *store.SongMetadata) {
	if err := s.store.SetSongMetadata(ctx, meta); err != nil {
		log.Errorf("%s Error setting metadata: %v", logcolors.LogCache, err)
	}
}

func (s *Server) addVideoID(ctx context.Context, cacheKey, videoID string) {
	if err := s.store.AddVideoID(ctx, cacheKey, videoID); err != nil {
		log.Errorf("%s Error adding videoId: %v", logcolors.LogCache, err)
	}
}

func (s *Server) getAllVideoIDsForSong(ctx context.Context, songName, artistName string) []string {
	vids, err := s.store.GetAllVideoIDsForSong(ctx, songName, artistName)
	if err != nil {
		log.Errorf("%s Error reading videoIds for song: %v", logcolors.LogCache, err)
		return nil
	}
	return vids
}

// videoIDsFunc adapts getAllVideoIDsForSong to the signature proxy.RevalidateAllForSong expects.
func (s *Server) videoIDsFunc(ctx context.Context) func(string, string) []string {
	return func(song, artist string) []string {
		return s.getAllVideoIDsForSong(ctx, song, artist)
	}
}
