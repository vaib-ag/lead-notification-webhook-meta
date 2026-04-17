package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

// MetaWebhookRequest defines the structure of the incoming webhook from Meta Lead Ads.
type MetaWebhookRequest struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Time    int64  `json:"time"`
		Changes []struct {
			Value struct {
				FormID      string `json:"form_id"`
				LeadgenID   string `json:"leadgen_id"`
				CreatedTime int64  `json:"created_time"`
				PageID      string `json:"page_id"`
				AdgroupID   string `json:"adgroup_id"`
				AdID        string `json:"ad_id"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// LeadDetails represents the data returned by the Meta Graph API for a specific lead.
type LeadDetails struct {
	ID          string `json:"id"`
	CreatedTime string `json:"created_time"`
	AdID        string `json:"ad_id"`
	FormID      string `json:"form_id"`
	FieldData   []struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	} `json:"field_data"`
}

// CRMLeadRequest represents the body expected by the Bacchapanti CRM API.
type CRMLeadRequest struct {
	ExternalID   string     `json:"external_id"`
	SourceSystem string     `json:"source_system"`
	User         CRMUser    `json:"user"`
	Student      CRMStudent `json:"student"`
}

type CRMUser struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Country   string `json:"country"`
	Timezone  string `json:"timezone"`
}

type CRMStudent struct {
	AgeGroup      string `json:"age_group"`
	LearningLevel string `json:"learning_level"`
	CampaignName  string `json:"campaign_name"`
	Notes         string `json:"notes"`
}

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: No .env file found, relying on system environment variables.")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8654"
	}

	mux := http.NewServeMux()

	// Webhook endpoint
	mux.HandleFunc("/webhook", handleWebhook)

	fmt.Printf("Starting Meta Webhook server on port %s...\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// handleWebhook handles both the verification GET and the notification POST.
func handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		verifyWebhook(w, r)
	case http.MethodPost:
		receiveNotification(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// verifyWebhook processes Meta's verification challenge.
func verifyWebhook(w http.ResponseWriter, r *http.Request) {
	verifyToken := os.Getenv("META_VERIFY_TOKEN")

	query := r.URL.Query()
	mode := query.Get("hub.mode")
	token := query.Get("hub.verify_token")
	challenge := query.Get("hub.challenge")

	if mode == "subscribe" && token == verifyToken {
		fmt.Println("Webhook verified successfully!")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
	} else {
		fmt.Println("Verification failed: Invalid token or mode")
		w.WriteHeader(http.StatusForbidden)
	}
}

// receiveNotification processes the lead notification POST request.
func receiveNotification(w http.ResponseWriter, r *http.Request) {
	appSecret := os.Getenv("META_APP_SECRET")

	// Read full body for signature validation and parsing
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Validate Meta's signature if APP_SECRET is provided
	if appSecret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if !validateSignature(body, signature, appSecret) {
			fmt.Println("Invalid X-Hub-Signature-256")
			http.Error(w, "Invalid signature", http.StatusForbidden)
			return
		}
	}

	var payload MetaWebhookRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("Error unmarshaling JSON: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// Meta Lead Ads webhook object should be "page"
	if payload.Object != "page" {
		w.WriteHeader(http.StatusOK) // Still return 200 to acknowledge
		return
	}

	// Process each leadgen change
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field == "leadgen" {
				leadgenID := change.Value.LeadgenID
				log.Printf("Received new lead! Leadgen ID: %s", leadgenID)

				// Fire-and-forget or synchronous fetch:
				// The user wants to fetch leads details "immediately after"
				go fetchAndProcessLead(leadgenID)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("EVENT_RECEIVED"))
}

// validateSignature checks if the payload matches the provided HMAC signature.
func validateSignature(payload []byte, signature, secret string) bool {
	if signature == "" || len(signature) < 8 { // format is sha256=xyz
		return false
	}

	// Extract hash from signature (skip "sha256=")
	actualHash := signature[7:]

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expectedHash := hex.EncodeToString(h.Sum(nil))

	return hmac.Equal([]byte(actualHash), []byte(expectedHash))
}

// fetchAndProcessLead calls the Meta Graph API to get the lead details.
func fetchAndProcessLead(leadID string) {
	accessToken := os.Getenv("META_PAGE_ACCESS_TOKEN")
	if accessToken == "" {
		log.Println("Missing META_PAGE_ACCESS_TOKEN, cannot fetch lead details.")
		return
	}

	// Construct Graph API URL (v20.0 is current at time of writing)
	url := fmt.Sprintf("https://graph.facebook.com/v20.0/%s?access_token=%s", leadID, accessToken)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error calling Meta API for lead %s: %v", leadID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Meta API returned non-OK status: %s, Body: %s", resp.Status, string(body))
		return
	}

	var lead LeadDetails
	if err := json.NewDecoder(resp.Body).Decode(&lead); err != nil {
		log.Printf("Error decoding lead details for %s: %v", leadID, err)
		return
	}

	// Log received lead details (email, phone, etc.)
	log.Printf("Successfully fetched details for lead %s:", leadID)

	// Convert Meta field_data back to a map for easier access
	fields := make(map[string]string)
	for _, fd := range lead.FieldData {
		if len(fd.Values) > 0 {
			fields[fd.Name] = fd.Values[0]
			log.Printf("  %s: %s", fd.Name, fd.Values[0])
		}
	}

	// Prepare CRM payload
	firstName := fields["first_name"]
	lastName := fields["last_name"]
	if firstName == "" && fields["full_name"] != "" {
		// Basic split if only full_name is provided
		n, _ := fmt.Sscanf(fields["full_name"], "%s %s", &firstName, &lastName)
		if n < 1 {
			firstName = fields["full_name"]
		}
	}

	crmReq := CRMLeadRequest{
		ExternalID:   lead.ID,
		SourceSystem: "meta_lead_ads",
		User: CRMUser{
			FirstName: firstName,
			LastName:  lastName,
			Email:     fields["email"],
			Phone:     fields["phone_number"], // Meta's default field name is usually phone_number
			Country:   fields["country"],
			Timezone:  "Asia/Kolkata", // Defaulting as per example or extracting if available
		},
		Student: CRMStudent{
			AgeGroup:      fields["age_group"],      // Custom fields from form if defined
			LearningLevel: fields["learning_level"], // Custom fields from form if defined
			CampaignName:  lead.FormID,              // Using Form ID as campaign placeholder
			Notes:         "Imported from Meta Lead Ads",
		},
	}

	pushToCRM(crmReq)
}

func pushToCRM(crmReq CRMLeadRequest) {
	apiKey := os.Getenv("CRM_API_KEY")
	apiURL := os.Getenv("CRM_API_URL") // e.g. https://bacchapanti.perfeasy.com/api/public/leads/ingest
	if apiKey == "" || apiURL == "" {
		log.Println("Missing CRM_API_KEY or CRM_API_URL, skipping CRM push.")
		return
	}

	jsonData, err := json.Marshal(crmReq)
	if err != nil {
		log.Printf("Error marshaling CRM request: %v", err)
		return
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error creating CRM request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending lead to CRM: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		log.Printf("Successfully pushed lead %s to CRM", crmReq.ExternalID)
	} else {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("CRM API returned error: %s, Body: %s", resp.Status, string(body))
	}
}
