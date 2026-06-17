// asc: App Store Connect bootstrap for smachnogo. Idempotently creates the
// bundle id (+ Sign in with Apple capability) and the subscription tree the
// code expects (group Premium; smachnogo.premium.monthly $6.99;
// smachnogo.premium.annual $39.99 with a 7-day free trial), and enables
// Billing Grace Period. Everything is ensure-style: safe to re-run.
//
//	go run . probe       — verify the API key works, list apps
//	go run . bootstrap   — full run (skips subscription steps until the
//	                       app record exists — that's web-only)
//
// Env: ASC_KEY_ID, ASC_KEY_PATH, ASC_ISSUER_ID (empty = individual key).
// Stdlib only: ES256 JWT is hand-rolled (r||s signature, not ASN.1).
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	baseURL  = "https://api.appstoreconnect.apple.com"
	bundleID = "app.smachnogo.ios"
	appName  = "smachnogo"

	groupName     = "Premium"
	monthlyID     = "smachnogo.premium.monthly"
	annualID      = "smachnogo.premium.annual"
	monthlyPrice  = "6.99"
	annualPrice   = "39.99"
	reviewNote    = "Unlocks photo scanning — point the camera at a meal for calories and nutrition. The text diary stays free without it."
	monthlyName   = "Premium Monthly"
	annualName    = "Premium Annual"
	subscDescript = "Photo calorie & nutrition scanning"
)

func main() {
	cmd := "probe"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	c, err := newClient()
	if err != nil {
		fatal(err)
	}
	switch cmd {
	case "probe":
		err = c.probe()
	case "bootstrap":
		err = c.bootstrap()
	case "token":
		var tok string
		if tok, err = c.jwt(); err == nil {
			fmt.Println(tok)
		}
	default:
		err = fmt.Errorf("unknown command %q (probe|bootstrap|token)", cmd)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}

// ---------- client / auth ----------

type client struct {
	keyID    string
	issuerID string // empty → individual-key JWT (sub: "user")
	key      *ecdsa.PrivateKey
	http     *http.Client
}

func newClient() (*client, error) {
	keyID := os.Getenv("ASC_KEY_ID")
	keyPath := os.Getenv("ASC_KEY_PATH")
	if keyID == "" || keyPath == "" {
		return nil, fmt.Errorf("ASC_KEY_ID and ASC_KEY_PATH are required")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s: not PEM", keyPath)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	ec, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ECDSA", parsed)
	}
	return &client{
		keyID:    keyID,
		issuerID: os.Getenv("ASC_ISSUER_ID"),
		key:      ec,
		http:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (c *client) jwt() (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "ES256", "kid": c.keyID, "typ": "JWT"}
	payload := map[string]any{
		"aud": "appstoreconnect-v1",
		"iat": now.Unix(),
		"exp": now.Add(15 * time.Minute).Unix(),
	}
	if c.issuerID != "" {
		payload["iss"] = c.issuerID
	} else {
		payload["sub"] = "user" // individual API key
	}
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(payload)
	signing := b64(h) + "." + b64(p)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", err
	}
	// JWT ES256 wants raw r||s, 32 bytes each — not ASN.1 DER.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + b64(sig), nil
}

type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("asc %d: %s", e.Status, e.Body) }

func (c *client) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, rdr)
	if err != nil {
		return err
	}
	tok, err := c.jwt()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Body: string(data)}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ---------- generic JSON:API shapes ----------

type resource struct {
	Type          string                     `json:"type"`
	ID            string                     `json:"id,omitempty"`
	Attributes    map[string]any             `json:"attributes,omitempty"`
	Relationships map[string]relationshipOne `json:"relationships,omitempty"`
}

type relationshipOne struct {
	Data refData `json:"data"`
}

type refData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type listResponse struct {
	Data []struct {
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	} `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

type oneResponse struct {
	Data struct {
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	} `json:"data"`
}

func rel(typ, id string) relationshipOne {
	return relationshipOne{Data: refData{Type: typ, ID: id}}
}

func isConflict(err error) bool {
	var ae *apiError
	if ok := asAPIErr(err, &ae); ok {
		return ae.Status == 409
	}
	return false
}

func asAPIErr(err error, out **apiError) bool {
	ae, ok := err.(*apiError)
	if ok {
		*out = ae
	}
	return ok
}

// ---------- commands ----------

func (c *client) probe() error {
	var apps listResponse
	if err := c.do("GET", "/v1/apps?limit=20", nil, &apps); err != nil {
		return err
	}
	fmt.Printf("auth OK (%s key)\n", map[bool]string{true: "team", false: "individual"}[c.issuerID != ""])
	if len(apps.Data) == 0 {
		fmt.Println("no app records yet")
	}
	for _, a := range apps.Data {
		fmt.Printf("app: %v (%v) id=%s\n", a.Attributes["name"], a.Attributes["bundleId"], a.ID)
	}
	return nil
}

func (c *client) bootstrap() error {
	if err := c.ensureBundleID(); err != nil {
		return fmt.Errorf("bundle id: %w", err)
	}
	appID, err := c.findApp()
	if err != nil {
		return fmt.Errorf("find app: %w", err)
	}
	if appID == "" {
		fmt.Println()
		fmt.Println("⏸  No app record for " + bundleID + " yet — that's the one piece the API can't create.")
		fmt.Println("   Web: App Store Connect → Apps → + → New App:")
		fmt.Println("     platform iOS · name \"smachnogo\" · primary language English (U.S.)")
		fmt.Println("     bundle ID app.smachnogo.ios · SKU smachnogo-ios")
		fmt.Println("   Then re-run: go run . bootstrap")
		return nil
	}
	fmt.Println("app record:", appID)
	groupID, err := c.ensureGroup(appID)
	if err != nil {
		return fmt.Errorf("subscription group: %w", err)
	}
	monthly, err := c.ensureSubscription(groupID, monthlyID, monthlyName, "ONE_MONTH")
	if err != nil {
		return fmt.Errorf("monthly: %w", err)
	}
	annual, err := c.ensureSubscription(groupID, annualID, annualName, "ONE_YEAR")
	if err != nil {
		return fmt.Errorf("annual: %w", err)
	}
	// Availability MUST precede price: a price point for a territory the
	// subscription isn't available in is rejected (409). USA-only matches
	// the app's availability — one territory, one price, metadata complete.
	for _, sub := range []string{monthly, annual} {
		if err := c.ensureAvailability(sub); err != nil {
			return fmt.Errorf("availability: %w", err)
		}
	}
	if err := c.ensurePrice(monthly, monthlyPrice); err != nil {
		return fmt.Errorf("monthly price: %w", err)
	}
	if err := c.ensurePrice(annual, annualPrice); err != nil {
		return fmt.Errorf("annual price: %w", err)
	}
	if err := c.ensureTrial(annual); err != nil {
		return fmt.Errorf("annual trial: %w", err)
	}
	if err := c.enableGracePeriod(appID); err != nil {
		return fmt.Errorf("grace period: %w", err)
	}
	fmt.Println()
	fmt.Println("✅ bootstrap complete (re-run safe). Family Sharing left OFF by design.")
	fmt.Println("⚠️  Subscriptions still need a REVIEW SCREENSHOT (scripts upload it) and,")
	fmt.Println("    critically, an ACTIVE Paid Applications Agreement (App Store Connect →")
	fmt.Println("    Business) — without it StoreKit returns zero products, even in sandbox.")
	return nil
}

// ---------- steps ----------

func (c *client) ensureBundleID() error {
	var list listResponse
	if err := c.do("GET", "/v1/bundleIds?filter[identifier]="+bundleID, nil, &list); err != nil {
		return err
	}
	var id string
	for _, b := range list.Data {
		// The filter matches prefixes — require exact.
		if b.Attributes["identifier"] == bundleID {
			id = b.ID
		}
	}
	if id == "" {
		var created oneResponse
		err := c.do("POST", "/v1/bundleIds", map[string]any{"data": resource{
			Type:       "bundleIds",
			Attributes: map[string]any{"identifier": bundleID, "name": appName, "platform": "IOS"},
		}}, &created)
		if err != nil {
			return err
		}
		id = created.Data.ID
		fmt.Println("bundle id created:", bundleID)
	} else {
		fmt.Println("bundle id exists:", bundleID)
	}
	// Sign in with Apple capability (M8). 409 = already enabled.
	err := c.do("POST", "/v1/bundleIdCapabilities", map[string]any{"data": resource{
		Type:          "bundleIdCapabilities",
		Attributes:    map[string]any{"capabilityType": "APPLE_ID_AUTH"},
		Relationships: map[string]relationshipOne{"bundleId": rel("bundleIds", id)},
	}}, nil)
	if err != nil && !isConflict(err) {
		return fmt.Errorf("APPLE_ID_AUTH capability: %w", err)
	}
	fmt.Println("capability APPLE_ID_AUTH ensured")
	return nil
}

func (c *client) findApp() (string, error) {
	var list listResponse
	if err := c.do("GET", "/v1/apps?filter[bundleId]="+bundleID, nil, &list); err != nil {
		return "", err
	}
	for _, a := range list.Data {
		if a.Attributes["bundleId"] == bundleID {
			return a.ID, nil
		}
	}
	return "", nil
}

func (c *client) ensureGroup(appID string) (string, error) {
	var list listResponse
	if err := c.do("GET", "/v1/apps/"+appID+"/subscriptionGroups", nil, &list); err != nil {
		return "", err
	}
	for _, g := range list.Data {
		if g.Attributes["referenceName"] == groupName {
			fmt.Println("subscription group exists:", groupName)
			return g.ID, nil
		}
	}
	var created oneResponse
	err := c.do("POST", "/v1/subscriptionGroups", map[string]any{"data": resource{
		Type:          "subscriptionGroups",
		Attributes:    map[string]any{"referenceName": groupName},
		Relationships: map[string]relationshipOne{"app": rel("apps", appID)},
	}}, &created)
	if err != nil {
		return "", err
	}
	fmt.Println("subscription group created:", groupName)
	// Customer-facing group name.
	err = c.do("POST", "/v1/subscriptionGroupLocalizations", map[string]any{"data": resource{
		Type:          "subscriptionGroupLocalizations",
		Attributes:    map[string]any{"name": groupName, "locale": "en-US"},
		Relationships: map[string]relationshipOne{"subscriptionGroup": rel("subscriptionGroups", created.Data.ID)},
	}}, nil)
	if err != nil && !isConflict(err) {
		return "", fmt.Errorf("group localization: %w", err)
	}
	return created.Data.ID, nil
}

func (c *client) ensureSubscription(groupID, productID, name, period string) (string, error) {
	var list listResponse
	if err := c.do("GET", "/v1/subscriptionGroups/"+groupID+"/subscriptions", nil, &list); err != nil {
		return "", err
	}
	for _, s := range list.Data {
		if s.Attributes["productId"] == productID {
			fmt.Println("subscription exists:", productID)
			return s.ID, nil
		}
	}
	var created oneResponse
	err := c.do("POST", "/v1/subscriptions", map[string]any{"data": resource{
		Type: "subscriptions",
		Attributes: map[string]any{
			"productId":          productID,
			"name":               name,
			"subscriptionPeriod": period,
			"familySharable":     false, // irreversible once on — stays off
			"groupLevel":         1,
			"reviewNote":         reviewNote,
		},
		Relationships: map[string]relationshipOne{"group": rel("subscriptionGroups", groupID)},
	}}, &created)
	if err != nil {
		return "", err
	}
	fmt.Println("subscription created:", productID)
	err = c.do("POST", "/v1/subscriptionLocalizations", map[string]any{"data": resource{
		Type:          "subscriptionLocalizations",
		Attributes:    map[string]any{"name": name, "description": subscDescript, "locale": "en-US"},
		Relationships: map[string]relationshipOne{"subscription": rel("subscriptions", created.Data.ID)},
	}}, nil)
	if err != nil && !isConflict(err) {
		return "", fmt.Errorf("localization: %w", err)
	}
	return created.Data.ID, nil
}

// ensureAvailability makes the subscription available in the USA only (POST
// replaces any prior availability). Matches the app's USA-only reach; a
// single available territory means a single price completes the metadata.
func (c *client) ensureAvailability(subID string) error {
	err := c.do("POST", "/v1/subscriptionAvailabilities", map[string]any{"data": map[string]any{
		"type":       "subscriptionAvailabilities",
		"attributes": map[string]any{"availableInNewTerritories": false},
		"relationships": map[string]any{
			"subscription":         map[string]any{"data": refData{Type: "subscriptions", ID: subID}},
			"availableTerritories": map[string]any{"data": []refData{{Type: "territories", ID: "USA"}}},
		},
	}}, nil)
	if err != nil {
		return err
	}
	fmt.Println("availability set (USA) for", subID)
	return nil
}

// ensurePrice sets the USA price. Availability must already exist (see
// ensureAvailability) or the price point is rejected 409. NOTE: a 409 here
// is a REAL failure — an earlier version swallowed it via isConflict and
// printed a false success while no price was ever stored.
func (c *client) ensurePrice(subID, price string) error {
	var existing listResponse
	if err := c.do("GET", "/v1/subscriptions/"+subID+"/prices?limit=1", nil, &existing); err == nil && len(existing.Data) > 0 {
		fmt.Println("price exists for", subID)
		return nil
	}
	pointID, err := c.findPricePoint(subID, price)
	if err != nil {
		return err
	}
	if err := c.do("POST", "/v1/subscriptionPrices", map[string]any{"data": resource{
		Type: "subscriptionPrices",
		Relationships: map[string]relationshipOne{
			"subscription":           rel("subscriptions", subID),
			"territory":              rel("territories", "USA"),
			"subscriptionPricePoint": rel("subscriptionPricePoints", pointID),
		},
	}}, nil); err != nil {
		return fmt.Errorf("set price $%s: %w", price, err)
	}
	fmt.Printf("price set: $%s (USA)\n", price)
	return nil
}

func (c *client) findPricePoint(subID, price string) (string, error) {
	path := "/v1/subscriptions/" + subID + "/pricePoints?filter[territory]=USA&limit=200"
	for path != "" {
		var list listResponse
		if err := c.do("GET", path, nil, &list); err != nil {
			return "", err
		}
		for _, p := range list.Data {
			if p.Attributes["customerPrice"] == price {
				return p.ID, nil
			}
		}
		path = trimBase(list.Links.Next)
	}
	return "", fmt.Errorf("no USA price point with customerPrice=%s", price)
}

func trimBase(u string) string {
	if u == "" {
		return ""
	}
	if len(u) > len(baseURL) && u[:len(baseURL)] == baseURL {
		return u[len(baseURL):]
	}
	return u
}

func (c *client) ensureTrial(subID string) error {
	var existing listResponse
	if err := c.do("GET", "/v1/subscriptions/"+subID+"/introductoryOffers?limit=1", nil, &existing); err == nil && len(existing.Data) > 0 {
		fmt.Println("introductory offer exists")
		return nil
	}
	// Territory + price point omitted: a FREE_TRIAL needs no price and
	// applies to all territories.
	err := c.do("POST", "/v1/subscriptionIntroductoryOffers", map[string]any{"data": resource{
		Type: "subscriptionIntroductoryOffers",
		Attributes: map[string]any{
			"duration":        "ONE_WEEK",
			"offerMode":       "FREE_TRIAL",
			"numberOfPeriods": 1,
		},
		Relationships: map[string]relationshipOne{"subscription": rel("subscriptions", subID)},
	}}, nil)
	if err != nil && !isConflict(err) {
		return err
	}
	fmt.Println("7-day free trial ensured on annual")
	return nil
}

func (c *client) enableGracePeriod(appID string) error {
	var gp oneResponse
	if err := c.do("GET", "/v1/apps/"+appID+"/subscriptionGracePeriod", nil, &gp); err != nil {
		return err
	}
	if gp.Data.Attributes["optIn"] == true {
		fmt.Println("billing grace period already ON")
		return nil
	}
	err := c.do("PATCH", "/v1/subscriptionGracePeriods/"+gp.Data.ID, map[string]any{"data": resource{
		Type: "subscriptionGracePeriods",
		ID:   gp.Data.ID,
		Attributes: map[string]any{
			"optIn":        true,
			"sandboxOptIn": true,
			"duration":     "SIXTEEN_DAYS",
			"renewalType":  "ALL_RENEWALS",
		},
	}}, nil)
	if err != nil {
		return err
	}
	fmt.Println("billing grace period ENABLED (16 days, all renewals)")
	return nil
}
