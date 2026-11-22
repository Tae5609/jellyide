package metadata

import (
	"context"
	"strings"

	"yt-scraper/internal/library"
	"yt-scraper/internal/musicbrainz"
)

// LookupReleaseResponse is the /metadata/lookup/release/{mbid} response.
type LookupReleaseResponse struct {
	Release  map[string]interface{}     `json:"release"`
	CoverURL string                     `json:"cover_url"`
	Mapping  []musicbrainz.MappingEntry `json:"mapping"`
}

// SearchMusicBrainz searches the MusicBrainz release index. When q is
// empty the query defaults to the album artist and title.
func (s *Service) SearchMusicBrainz(ctx context.Context, albumID, q string) ([]musicbrainz.ReleaseSearchResult, error) {
	if q == "" {
		if albumID == "" {
			return nil, &ValidationError{Message: "q or album_id is required"}
		}
		album, err := s.lib.GetAlbum(albumID)
		if err != nil {
			return nil, &NotFoundError{Message: "album not found"}
		}
		q = strings.TrimSpace(album.Artist + " " + album.Title)
		if q == "" {
			return nil, &ValidationError{Message: "album has no name to search for"}
		}
	}
	return s.mb.SearchReleases(ctx, q)
}

// LookupRelease fetches a MusicBrainz release and maps it onto the album's
// local tracks. The UI applies the result through PATCH /metadata/tracks
// and cover/from-url; there is no separate apply endpoint.
func (s *Service) LookupRelease(ctx context.Context, albumID, mbid string) (*LookupReleaseResponse, error) {
	if albumID == "" {
		return nil, &ValidationError{Message: "album_id is required"}
	}
	if _, err := s.lib.GetAlbum(albumID); err != nil {
		return nil, &NotFoundError{Message: "album not found"}
	}

	release, err := s.mb.GetRelease(ctx, mbid)
	if err != nil {
		return nil, err
	}

	tracks, err := s.lib.GetTracksByAlbum(albumID)
	if err != nil {
		return nil, err
	}
	local := make([]musicbrainz.LocalTrack, 0, len(tracks))
	for i := range tracks {
		lt := musicbrainz.LocalTrack{TrackID: tracks[i].ID, Title: tracks[i].Title, Current: currentFieldMap(&tracks[i])}
		if tracks[i].TrackNumber != nil {
			lt.Number = *tracks[i].TrackNumber
			lt.HasNumber = true
		}
		if tracks[i].DiscNumber != nil {
			lt.Disc = *tracks[i].DiscNumber
		}
		local = append(local, lt)
	}

	return &LookupReleaseResponse{
		Release:  release.Raw,
		CoverURL: "https://coverartarchive.org/release/" + mbid + "/front",
		Mapping:  musicbrainz.MapTracks(release, local),
	}, nil
}

// currentFieldMap is the "current" side of a mapping entry.
func currentFieldMap(t *library.Track) map[string]interface{} {
	m := map[string]interface{}{"title": t.Title}
	if t.Artist != nil {
		m["artist"] = *t.Artist
	}
	if t.AlbumArtist != nil {
		m["album_artist"] = *t.AlbumArtist
	}
	if t.TrackNumber != nil {
		m["track_number"] = *t.TrackNumber
	}
	if t.DiscNumber != nil {
		m["disc_number"] = *t.DiscNumber
	}
	if t.Year != nil {
		m["year"] = *t.Year
	}
	if t.Genre != nil {
		m["genre"] = *t.Genre
	}
	if t.Composer != nil {
		m["composer"] = *t.Composer
	}
	if t.Comment != nil {
		m["comment"] = *t.Comment
	}
	return m
}
