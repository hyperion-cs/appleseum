package applebackemu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// This is a frozen expected request, independent of the client's query builder.
const (
	summaryQuery = `query requestAccountSummary {
 account { appleId familySharing { isHoh isInPaymentSharingFamily isNonHoHMemberInFamilyPlan isFamilyPaymentFlexMember } }
 authorizations { authorizedComputers linkedDevices }
 purchases { subscriptions preOrders }
 payments { balance balanceWithFreeContentCredit }
 settings { nickname }
 community { reviewsAndRatings ratingsAndReviewsBaseUrl }
}`
)

// StoreContext specifies expected headers. Storefront uses the finance form, e.g. 143517-2,13.
// Header overrides platform defaults and adds AMD or cookie requirements.
type StoreContext struct {
	Dsid       string
	Storefront string
	Header     http.Header
}

// JsonResponse encodes synthetic JSON; callers can override body or status.
func JsonResponse(value any) (Response, error) {
	body, err := json.Marshal(value)
	return Response{Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, err
}

// FinanceRequest checks an empty GET or JSON POST, ignoring key order and whitespace.
// Value types must match.
func FinanceRequest(context StoreContext, endpoint string, input any, response Response) (Step, error) {
	header := http.Header{
		"User-Agent": {"MacAppStore/3.0 MacAppStore/3.0 (Macintosh; OS X 26.5; 25F71) AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"},
		"Accept":     {"application/json"}, "Accept-Encoding": {"br, gzip, deflate"}, "Accept-Language": {"en-us"},
		"X-Apple-Client-Application": {"com.apple.AppStore"},
		"X-Mme-Client-Info":          {"<MacBookPro18,1> <macOS;26.5;25F71> <com.apple.AppStore/3.0 (com.apple.AppStore/3.0)>"},
		"X-Apple-Store-Front":        {context.Storefront}, "X-Dsid": {context.Dsid},
	}
	step := Step{Name: "finance request", Method: "GET", Url: endpoint, Header: header, Body: []byte{}, Response: response}
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return Step{}, err
		}
		expected, err := decodeJson(encoded)
		if err != nil {
			return Step{}, err
		}
		step.Method, step.Body = "POST", nil
		header.Set("Content-Type", "application/json")

		step.Check = func(request Request) error {
			actual, err := decodeJson(request.Body)
			if err != nil || !reflect.DeepEqual(actual, expected) {
				return fmt.Errorf("finance JSON body mismatch")
			}
			return nil
		}
	}
	for name, values := range context.Header {
		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return step, nil
}

func decodeJson(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("extra or invalid JSON data")
	}
	return value, nil
}

// PurchaseSettings advertises the supplied years without sorting or deduplication.
func PurchaseSettings(context StoreContext, years []int) (Step, error) {
	yearList := make([]map[string]int, len(years))
	for i, year := range years {
		yearList[i] = map[string]int{"year": year}
	}
	response, err := JsonResponse(map[string]any{"customDateFilter": map[string]any{"years": yearList}})
	if err != nil {
		return Step{}, err
	}
	step, err := FinanceRequest(context, "https://ph2.itunes.apple.com/settings", nil, response)
	step.Name = "purchase settings"
	return step, err
}

// PurchaseHistoryPage checks one APP/CUSTOM history request.
// Tests supply the response, including pagination and error cases.
func PurchaseHistoryPage(context StoreContext, year int, cursor string, response Response) (Step, error) {
	input := map[string]any{"searchCriteria": map[string]any{
		"dateRangeType": "CUSTOM", "customYear": year, "contentTypeGroups": []string{"APP"},
	}}
	if cursor != "" {
		input["paginationToken"] = cursor
	}
	step, err := FinanceRequest(context, "https://ph2.itunes.apple.com/purchaseHistory", input, response)
	step.Name = "purchase history page"
	return step, err
}

// StoreLookup checks one public lookup batch in the specified country.
func StoreLookup(country string, ids []int64, response Response) (Step, error) {
	if country == "" || len(ids) == 0 {
		return Step{}, fmt.Errorf("applebackemu: lookup country and IDs required")
	}
	csv := make([]string, len(ids))
	for i, id := range ids {
		csv[i] = strconv.FormatInt(id, 10)
	}
	query := url.Values{"country": {country}, "entity": {"software"}, "id": {strings.Join(csv, ",")}}
	return Step{Name: "store lookup", Method: "GET", Url: "https://itunes.apple.com/lookup?" + query.Encode(), Body: []byte{}, Response: response}, nil
}

// AccountSummary checks the read-only GraphQL account request on its buy pod.
func AccountSummary(context StoreContext, pod string, response Response) (Step, error) {
	host := "buy.itunes.apple.com"
	if pod != "" {
		host = "p" + pod + "-buy.itunes.apple.com"
	}
	step, err := FinanceRequest(context, "https://"+host+"/account/v1/summary/graphql", map[string]string{"query": summaryQuery}, response)
	step.Name = "account summary"
	return step, err
}
