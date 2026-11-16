package library

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"github.com/hyperion-cs/appleseum/applekit"
	"github.com/hyperion-cs/appleseum/applekit/ipa"

	"appleseum/backend/internal/artwork"
	"appleseum/backend/internal/db"
)

const (
	artworkWorkers = 4
)

// Icons stores app icons as content-addressed files that app_assets rows refer to.
type Icons struct {
	db     *db.Db
	fetch  func(context.Context, string) ([]byte, error)
	report func(string, error)
	// A file is written or removed only together with the rows that refer to it.
	mu sync.Mutex
}

// IconUrl is where the API serves an icon, or "" for a name that is not one.
func IconUrl(name string) string {
	if !artwork.ValidName(name) {
		return ""
	}
	return "/api/icons/" + name
}

func (i *Icons) present(name string) bool {
	if !artwork.ValidName(name) {
		return false
	}
	_, err := os.Stat(i.db.IconPath(name))
	return err == nil
}

// FromCatalog fetches App Store artwork for apps whose icon is missing or changed.
func (i *Icons) FromCatalog(ctx context.Context, owner string, apps []applekit.CatalogApp, progress func(float64)) {
	existing, err := i.db.Assets(ctx, owner)
	if err != nil {
		i.report("artwork_read", err)
		return
	}
	var completed atomic.Int64
	tasks := make(chan applekit.CatalogApp)
	var workers sync.WaitGroup
	for range artworkWorkers {
		workers.Go(func() {
			for app := range tasks {
				cached := existing[app.Id]
				if ctx.Err() == nil && app.ArtworkUrl != "" && !(cached.Source == app.ArtworkUrl && i.present(cached.Icon)) {
					if raw, err := i.fetch(ctx, app.ArtworkUrl); err == nil {
						i.save(ctx, owner, app.Id, raw, app.ArtworkUrl)
					}
				}
				progress(100 * float64(completed.Add(1)) / float64(len(apps)))
			}
		})
	}
	for _, app := range apps {
		if ctx.Err() != nil {
			break
		}
		tasks <- app
	}
	close(tasks)
	workers.Wait()
	progress(100)
}

// FromIpa extracts the icon of an imported or downloaded app that has none yet.
func (i *Icons) FromIpa(ctx context.Context, owner string, appId int64, path string) {
	if icon, err := i.db.AssetIcon(ctx, owner, appId); err == nil && i.present(icon) {
		return
	}
	if raw, err := ipa.Artwork(path); err == nil {
		i.save(ctx, owner, appId, raw, "")
	}
}

func (i *Icons) save(ctx context.Context, owner string, appId int64, raw []byte, source string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	name, err := artwork.Save(i.db.Directory(), raw)
	if err == nil {
		err = i.db.SaveAsset(ctx, owner, appId, name, source)
	}
	i.report("artwork_save", err)
}

// ClearCatalog forgets the purchase list and deletes icons nothing refers to anymore.
func (i *Icons) ClearCatalog(ctx context.Context, owner string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	unused, err := i.db.ClearCatalog(ctx, owner)
	if err != nil {
		return err
	}
	for _, name := range unused {
		if !artwork.ValidName(name) {
			continue
		}
		if err := os.Remove(i.db.IconPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
