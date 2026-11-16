package storekit

import "context"

func (s *Session) DelistedIosApps(ctx context.Context) ([]Purchase, error) {
	apps, err := s.ListPurchases(ctx)
	if err != nil {
		return nil, err
	}
	listings, err := lookupStoreApps(ctx, s.Client, CountryFromStorefront(s.Account.StoreFront), appIds(apps), nil)
	if err != nil {
		return nil, err
	}
	out := []Purchase{}
	for _, p := range apps {
		if _, listed := listings[p.Id]; !listed {
			out = append(out, p)
		}
	}
	return out, nil
}

func appIds(purchases []Purchase) []int64 {
	ids := make([]int64, len(purchases))
	for i, p := range purchases {
		ids[i] = p.Id
	}
	return ids
}

type CatalogApp struct {
	PurchasedAt string
	Size        *int64
	ArtworkUrl  string
	Id          int64
	Name        string
	Delisted    bool
}

func (s *Session) Catalog(ctx context.Context, progress func(float64)) ([]CatalogApp, error) {
	report := func(value float64) {
		if progress != nil {
			progress(value)
		}
	}

	report(0)
	owned, err := s.listPurchases(ctx, func(value float64) { report(value * 0.7) })
	if err != nil {
		return nil, err
	}
	apps := make([]CatalogApp, len(owned))
	for i, p := range owned {
		apps[i] = CatalogApp{Id: p.Id, Name: p.Name, PurchasedAt: p.Date, ArtworkUrl: p.ArtworkUrl}
	}
	listings, err := lookupStoreApps(ctx, s.Client, CountryFromStorefront(s.Account.StoreFront), appIds(owned), func(value float64) { report(70 + value*0.3) })
	if err != nil {
		return nil, err
	}
	for i := range apps {
		listing, listed := listings[apps[i].Id]
		apps[i].Delisted = !listed
		apps[i].Size = listing.Size
		if listing.ArtworkUrl != "" {
			apps[i].ArtworkUrl = listing.ArtworkUrl
		}
	}
	return apps, nil
}
