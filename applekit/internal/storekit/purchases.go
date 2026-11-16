package storekit

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Purchase struct {
	ArtworkUrl string
	Id         int64
	Name       string
	Kind       string
	Price      string
	Date       string
}

type purchaseCriteria struct {
	DateRangeType     string   `json:"dateRangeType"`
	CustomYear        int      `json:"customYear"`
	ContentTypeGroups []string `json:"contentTypeGroups"`
}

type purchasePage struct {
	PaginationToken string `json:"paginationToken"`
	Purchases       *[]struct {
		InvoiceDate  string `json:"invoiceDate"`
		WebOrderDate string `json:"webOrderDate"`
		LineItems    []struct {
			Id         json.RawMessage `json:"adamId"`
			Kind       string          `json:"kind"`
			Name       string          `json:"nameForDisplay"`
			Price      string          `json:"price"`
			Date       string          `json:"purchaseDate"`
			ArtworkUrl json.RawMessage `json:"artworkUrl"`
			CoverArt   struct {
				Url string `json:"url"`
			} `json:"coverArt"`
		} `json:"lineItems"`
	} `json:"purchases"`
}

// ListPurchases returns unique iOS apps in purchase-history order, including delisted apps.
// Apple's APP filter also includes Mac apps and subscriptions.
func (s *Session) ListPurchases(ctx context.Context) ([]Purchase, error) {
	return s.listPurchases(ctx, nil)
}

func (s *Session) listPurchases(ctx context.Context, progress func(float64)) ([]Purchase, error) {
	var settings struct {
		Filter *struct {
			Years *[]struct {
				Year int `json:"year"`
			} `json:"years"`
		} `json:"customDateFilter"`
	}
	if err := s.financeJson(ctx, "https://ph2.itunes.apple.com/settings", nil, &settings, "purchases"); err != nil {
		return nil, err
	}
	if settings.Filter == nil || settings.Filter.Years == nil || len(*settings.Filter.Years) > 200 {
		return nil, fmt.Errorf("purchase settings have no valid year list")
	}
	years := []int{}
	for _, v := range *settings.Filter.Years {
		if v.Year < 1 || v.Year > s.now().Year()+1 {
			return nil, fmt.Errorf("purchase settings contain an invalid year")
		}
		years = append(years, v.Year)
	}
	slices.SortFunc(years, func(a, b int) int { return cmp.Compare(b, a) })
	years = slices.Compact(years)
	s.authenticated()
	out := []Purchase{}
	seenApps := map[int64]bool{}
	for index, year := range years {
		cursor := ""
		seenCursors := map[string]bool{}
		for page := 1; ; page++ {
			input := struct {
				Criteria purchaseCriteria `json:"searchCriteria"`
				Cursor   string           `json:"paginationToken,omitempty"`
			}{purchaseCriteria{"CUSTOM", year, []string{"APP"}}, cursor}
			var response purchasePage
			if err := s.financeJson(ctx, "https://ph2.itunes.apple.com/purchaseHistory", input, &response, "purchases"); err != nil {
				return nil, err
			}
			if err := response.appendPurchases(seenApps, &out); err != nil {
				return nil, err
			}
			s.authenticated()
			cursor = response.PaginationToken
			if cursor == "" {
				break
			}
			if seenCursors[cursor] || page == 1_000 {
				return nil, fmt.Errorf("purchase pagination repeated or exceeded the limit for year %d", year)
			}
			seenCursors[cursor] = true
		}
		if progress != nil {
			progress(100 * float64(index+1) / float64(len(years)))
		}
	}
	if len(years) == 0 && progress != nil {
		progress(100)
	}
	return out, nil
}

func (page purchasePage) appendPurchases(seen map[int64]bool, out *[]Purchase) error {
	if page.Purchases == nil {
		return fmt.Errorf("purchase response has no purchases array")
	}
	for _, order := range *page.Purchases {
		for _, item := range order.LineItems {
			if item.Kind != "IOSApp" {
				continue
			}
			var number json.Number
			if err := json.Unmarshal(item.Id, &number); err != nil {
				return fmt.Errorf("purchase contains an invalid iOS app ID")
			}
			id, err := strconv.ParseInt(string(number), 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("purchase contains an invalid iOS app ID")
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			date := purchaseDay(item.Date)
			if date == "" {
				date = purchaseDay(order.InvoiceDate)
			}
			if date == "" {
				date = purchaseDay(order.WebOrderDate)
			}
			var template string
			_ = json.Unmarshal(item.ArtworkUrl, &template)
			artwork := purchaseArtworkUrl(template)
			if artwork == "" {
				artwork = purchaseArtworkUrl(item.CoverArt.Url)
			}
			name := item.Name
			if name == "" {
				name = string(number)
			}
			*out = append(*out, Purchase{Id: id, Name: name, Kind: "iOS App", Price: item.Price, Date: date, ArtworkUrl: artwork})
		}
	}
	return nil
}

// ph2 dates have no timezone; preserve the calendar day instead of inventing an instant.
func purchaseDay(value string) string {
	value = strings.Replace(value, " Sept ", " Sep ", 1)
	for _, layout := range []string{time.DateOnly, time.RFC3339, "2 January 2006 15:04", "2 Jan 2006 15:04", "2 January 2006", "2 Jan 2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.DateOnly)
		}
	}
	return ""
}

var purchaseArtworkTemplate = strings.NewReplacer("{w}", "120", "{h}", "120", "{c}", "bb", "-{q}", "", "{q}", "", "{f}", "jpg")

func purchaseArtworkUrl(template string) string {
	if len(template) == 0 || len(template) > 4_096 {
		return ""
	}
	raw := purchaseArtworkTemplate.Replace(template)
	if strings.ContainsAny(raw, "{}") {
		return ""
	}
	return raw
}
