package onvif

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Debug controls verbose ONVIF logging. Set via the parent server using the DEBUG env or flag.
var Debug bool

const debugPayloadLimit = 8192

func debugf(format string, args ...interface{}) {
	if !Debug {
		return
	}
	log.Printf("[onvif] "+format, args...)
}

func truncateForDebug(input string) string {
	if len(input) > debugPayloadLimit {
		return input[:debugPayloadLimit] + "...(truncated)"
	}
	return input
}

// Client represents an ONVIF device client
type Client struct {
	endpoint string
	username string
	password string
	client   *http.Client
}

// NewClient creates a new ONVIF client
func NewClient(endpoint, username, password string) *Client {
	return &Client{
		endpoint: endpoint,
		username: username,
		password: password,
		client: &http.Client{
			// Pull-point event subscriptions rely on long-polling requests that routinely block
			// for ~15 seconds. Give the HTTP client a generous timeout so these calls can
			// complete rather than being canceled prematurely.
			Timeout: 45 * time.Second,
		},
	}
}

// generateSecurityHeader generates WS-Security header for ONVIF authentication
func (c *Client) generateSecurityHeader() string {
	nonce := make([]byte, 20)
	if _, err := rand.Read(nonce); err != nil {
		// Fallback to less secure but functional nonce if crypto/rand fails
		for i := range nonce {
			nonce[i] = byte(i + 1)
		}
	}
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	// Create password digest: Base64(SHA1(nonce + created + password))
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.password))
	passwordDigest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	nonceBase64 := base64.StdEncoding.EncodeToString(nonce)

	return fmt.Sprintf(`<Security xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
		<UsernameToken>
			<Username>%s</Username>
			<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</Password>
			<Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</Nonce>
			<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">%s</Created>
		</UsernameToken>
	</Security>`, c.username, passwordDigest, nonceBase64, created)
}

// buildSOAPEnvelope creates a SOAP envelope with the given body
func (c *Client) buildSOAPEnvelope(body string, authenticated bool) string {
	securityHeader := ""
	if authenticated && c.username != "" {
		securityHeader = c.generateSecurityHeader()
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope 
	xmlns:soap="http://www.w3.org/2003/05/soap-envelope" 
	xmlns:tds="http://www.onvif.org/ver10/device/wsdl"
	xmlns:trt="http://www.onvif.org/ver10/media/wsdl"
	xmlns:tt="http://www.onvif.org/ver10/schema"
	xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"
	xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
	xmlns:wsa="http://www.w3.org/2005/08/addressing">
	<soap:Header>%s</soap:Header>
	<soap:Body>%s</soap:Body>
</soap:Envelope>`, securityHeader, body)
}

// sendRequest sends a SOAP request to the ONVIF device
func (c *Client) sendRequest(serviceURL, action, body string, authenticated bool) ([]byte, error) {
	envelope := c.buildSOAPEnvelope(body, authenticated)
	if Debug {
		debugf("SOAP request to %s (%s): %s", serviceURL, action, truncateForDebug(envelope))
	}

	req, err := http.NewRequest("POST", serviceURL, bytes.NewBufferString(envelope))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", action)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if Debug {
		debugf("SOAP response from %s (%s): %s", serviceURL, action, truncateForDebug(string(respBody)))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ONVIF request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// DeviceInfo represents device information
type DeviceInfo struct {
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	FirmwareVersion string `json:"firmwareVersion"`
	SerialNumber    string `json:"serialNumber"`
	HardwareID      string `json:"hardwareId"`
}

// GetDeviceInformationResponse represents the response structure
type GetDeviceInformationResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetDeviceInformationResponse struct {
			Manufacturer    string `xml:"Manufacturer"`
			Model           string `xml:"Model"`
			FirmwareVersion string `xml:"FirmwareVersion"`
			SerialNumber    string `xml:"SerialNumber"`
			HardwareId      string `xml:"HardwareId"`
		} `xml:"GetDeviceInformationResponse"`
	} `xml:"Body"`
}

// GetDeviceInformation retrieves device information
func (c *Client) GetDeviceInformation() (*DeviceInfo, error) {
	body := `<tds:GetDeviceInformation/>`
	serviceURL := c.endpoint + "/onvif/device_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation", body, true)
	if err != nil {
		return nil, err
	}

	var response GetDeviceInformationResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	return &DeviceInfo{
		Manufacturer:    response.Body.GetDeviceInformationResponse.Manufacturer,
		Model:           response.Body.GetDeviceInformationResponse.Model,
		FirmwareVersion: response.Body.GetDeviceInformationResponse.FirmwareVersion,
		SerialNumber:    response.Body.GetDeviceInformationResponse.SerialNumber,
		HardwareID:      response.Body.GetDeviceInformationResponse.HardwareId,
	}, nil
}

// Profile represents a media profile
type Profile struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// PTZPosition represents the current PTZ coordinates for a camera
type PTZPosition struct {
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
}

// GetProfilesResponse represents the response structure
type GetProfilesResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetProfilesResponse struct {
			Profiles []struct {
				Token string `xml:"token,attr"`
				Name  string `xml:"Name"`
			} `xml:"Profiles"`
		} `xml:"GetProfilesResponse"`
	} `xml:"Body"`
}

// GetProfiles retrieves media profiles
func (c *Client) GetProfiles() ([]Profile, error) {
	body := `<trt:GetProfiles/>`
	serviceURL := c.endpoint + "/onvif/media_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/media/wsdl/GetProfiles", body, true)
	if err != nil {
		return nil, err
	}

	var response GetProfilesResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	profiles := make([]Profile, 0)
	for _, p := range response.Body.GetProfilesResponse.Profiles {
		profiles = append(profiles, Profile{
			Token: p.Token,
			Name:  p.Name,
		})
	}

	return profiles, nil
}

// GetStreamURIResponse represents the response structure
type GetStreamURIResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetStreamUriResponse struct {
			MediaUri struct {
				Uri string `xml:"Uri"`
			} `xml:"MediaUri"`
		} `xml:"GetStreamUriResponse"`
	} `xml:"Body"`
}

// GetStreamURI retrieves the stream URI for a profile
func (c *Client) GetStreamURI(profileToken string) (string, error) {
	body := fmt.Sprintf(`<trt:GetStreamUri>
		<trt:StreamSetup>
			<tt:Stream>RTP-Unicast</tt:Stream>
			<tt:Transport>
				<tt:Protocol>RTSP</tt:Protocol>
			</tt:Transport>
		</trt:StreamSetup>
		<trt:ProfileToken>%s</trt:ProfileToken>
	</trt:GetStreamUri>`, profileToken)

	serviceURL := c.endpoint + "/onvif/media_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/media/wsdl/GetStreamUri", body, true)
	if err != nil {
		return "", err
	}

	var response GetStreamURIResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return "", err
	}

	return response.Body.GetStreamUriResponse.MediaUri.Uri, nil
}

// GetSnapshotURIResponse represents the response structure
type GetSnapshotURIResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetSnapshotUriResponse struct {
			MediaUri struct {
				Uri string `xml:"Uri"`
			} `xml:"MediaUri"`
		} `xml:"GetSnapshotUriResponse"`
	} `xml:"Body"`
}

// GetSnapshotURI retrieves the snapshot URI for a profile
func (c *Client) GetSnapshotURI(profileToken string) (string, error) {
	body := fmt.Sprintf(`<trt:GetSnapshotUri>
		<trt:ProfileToken>%s</trt:ProfileToken>
	</trt:GetSnapshotUri>`, profileToken)

	serviceURL := c.endpoint + "/onvif/media_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/media/wsdl/GetSnapshotUri", body, true)
	if err != nil {
		return "", err
	}

	var response GetSnapshotURIResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return "", err
	}

	return response.Body.GetSnapshotUriResponse.MediaUri.Uri, nil
}

// PTZMove performs a continuous PTZ move
func (c *Client) PTZMove(profileToken string, panTilt, zoom struct{ X, Y float64 }) error {
	body := fmt.Sprintf(`<tptz:ContinuousMove>
		<tptz:ProfileToken>%s</tptz:ProfileToken>
		<tptz:Velocity>
			<tt:PanTilt x="%.2f" y="%.2f"/>
			<tt:Zoom x="%.2f"/>
		</tptz:Velocity>
	</tptz:ContinuousMove>`, profileToken, panTilt.X, panTilt.Y, zoom.X)

	serviceURL := c.endpoint + "/onvif/ptz_service"

	_, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/ContinuousMove", body, true)
	return err
}

// PTZStop stops PTZ movement
func (c *Client) PTZStop(profileToken string) error {
	body := fmt.Sprintf(`<tptz:Stop>
		<tptz:ProfileToken>%s</tptz:ProfileToken>
		<tptz:PanTilt>true</tptz:PanTilt>
		<tptz:Zoom>true</tptz:Zoom>
	</tptz:Stop>`, profileToken)

	serviceURL := c.endpoint + "/onvif/ptz_service"

	_, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/Stop", body, true)
	return err
}

// GetPTZStatus retrieves the current PTZ position for the given profile
func (c *Client) GetPTZStatus(profileToken string) (*PTZPosition, error) {
	body := fmt.Sprintf(`<tptz:GetStatus>
		<tptz:ProfileToken>%s</tptz:ProfileToken>
	</tptz:GetStatus>`, profileToken)

	serviceURL := c.endpoint + "/onvif/ptz_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/GetStatus", body, true)
	if err != nil {
		return nil, err
	}

	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			GetStatusResponse struct {
				PTZStatus struct {
					Position struct {
						PanTilt struct {
							X float64 `xml:"x,attr"`
							Y float64 `xml:"y,attr"`
						} `xml:"PanTilt"`
						Zoom struct {
							X float64 `xml:"x,attr"`
						} `xml:"Zoom"`
					} `xml:"Position"`
				} `xml:"PTZStatus"`
			} `xml:"GetStatusResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	position := response.Body.GetStatusResponse.PTZStatus.Position
	return &PTZPosition{
		Pan:  position.PanTilt.X,
		Tilt: position.PanTilt.Y,
		Zoom: position.Zoom.X,
	}, nil
}

// PTZGotoPreset goes to a PTZ preset
func (c *Client) PTZGotoPreset(profileToken, presetToken string) error {
	body := fmt.Sprintf(`<tptz:GotoPreset>
		<tptz:ProfileToken>%s</tptz:ProfileToken>
		<tptz:PresetToken>%s</tptz:PresetToken>
	</tptz:GotoPreset>`, profileToken, presetToken)

	serviceURL := c.endpoint + "/onvif/ptz_service"

	_, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/GotoPreset", body, true)
	return err
}

// GetServicesResponse represents the GetServices response
type GetServicesResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetServicesResponse struct {
			Service []struct {
				Namespace string `xml:"Namespace"`
				XAddr     string `xml:"XAddr"`
			} `xml:"Service"`
		} `xml:"GetServicesResponse"`
	} `xml:"Body"`
}

// Service represents an ONVIF service
type Service struct {
	Namespace string `json:"namespace"`
	XAddr     string `json:"xAddr"`
}

// GetServices retrieves available ONVIF services
func (c *Client) GetServices() ([]Service, error) {
	body := `<tds:GetServices><tds:IncludeCapability>false</tds:IncludeCapability></tds:GetServices>`
	serviceURL := c.endpoint + "/onvif/device_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/device/wsdl/GetServices", body, true)
	if err != nil {
		return nil, err
	}

	var response GetServicesResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	services := make([]Service, 0)
	for _, s := range response.Body.GetServicesResponse.Service {
		services = append(services, Service{
			Namespace: s.Namespace,
			XAddr:     s.XAddr,
		})
	}

	return services, nil
}

// GetCapabilitiesResponse represents the GetCapabilities response
type GetCapabilitiesResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetCapabilitiesResponse struct {
			Capabilities struct {
				PTZ struct {
					XAddr string `xml:"XAddr"`
				} `xml:"PTZ"`
				Media struct {
					XAddr string `xml:"XAddr"`
				} `xml:"Media"`
				Device struct {
					XAddr string `xml:"XAddr"`
				} `xml:"Device"`
				Events struct {
					XAddr string `xml:"XAddr"`
				} `xml:"Events"`
			} `xml:"Capabilities"`
		} `xml:"GetCapabilitiesResponse"`
	} `xml:"Body"`
}

// Capabilities represents device capabilities
type Capabilities struct {
	PTZAddr    string `json:"ptzAddr"`
	MediaAddr  string `json:"mediaAddr"`
	DeviceAddr string `json:"deviceAddr"`
	EventsAddr string `json:"eventsAddr"`
}

// GetCapabilities retrieves device capabilities
func (c *Client) GetCapabilities() (*Capabilities, error) {
	body := `<tds:GetCapabilities><tds:Category>All</tds:Category></tds:GetCapabilities>`
	serviceURL := c.endpoint + "/onvif/device_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver10/device/wsdl/GetCapabilities", body, true)
	if err != nil {
		return nil, err
	}

	var response GetCapabilitiesResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	return &Capabilities{
		PTZAddr:    response.Body.GetCapabilitiesResponse.Capabilities.PTZ.XAddr,
		MediaAddr:  response.Body.GetCapabilitiesResponse.Capabilities.Media.XAddr,
		DeviceAddr: response.Body.GetCapabilitiesResponse.Capabilities.Device.XAddr,
		EventsAddr: response.Body.GetCapabilitiesResponse.Capabilities.Events.XAddr,
	}, nil
}

// InjectCredentials adds username/password to an RTSP URL
func InjectCredentials(rtspURL, username, password string) (string, error) {
	if username == "" || password == "" {
		return rtspURL, nil
	}

	parsed, err := url.Parse(rtspURL)
	if err != nil {
		return "", err
	}

	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}

// GetPresetsResponse represents the GetPresets response
type GetPresetsResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GetPresetsResponse struct {
			Preset []struct {
				Token string `xml:"token,attr"`
				Name  string `xml:"Name"`
			} `xml:"Preset"`
		} `xml:"GetPresetsResponse"`
	} `xml:"Body"`
}

// Preset represents a PTZ preset
type Preset struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// GetPresets retrieves PTZ presets
func (c *Client) GetPresets(profileToken string) ([]Preset, error) {
	body := fmt.Sprintf(`<tptz:GetPresets>
		<tptz:ProfileToken>%s</tptz:ProfileToken>
	</tptz:GetPresets>`, profileToken)

	serviceURL := c.endpoint + "/onvif/ptz_service"

	resp, err := c.sendRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/GetPresets", body, true)
	if err != nil {
		return nil, err
	}

	var response GetPresetsResponse
	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	presets := make([]Preset, 0)
	for _, p := range response.Body.GetPresetsResponse.Preset {
		presets = append(presets, Preset{
			Token: p.Token,
			Name:  p.Name,
		})
	}

	return presets, nil
}

// PullPointSubscription represents a pull-point subscription details
type PullPointSubscription struct {
	ReferenceAddress string
	TerminationTime  time.Time
}

// EventMessage represents a parsed ONVIF event
type EventMessage struct {
	Topic   string
	Message string
	UtcTime time.Time
}

// CreatePullPointSubscription creates a pull-point subscription on the events service
func (c *Client) CreatePullPointSubscription(eventsURL string) (*PullPointSubscription, error) {
	body := `<wsnt:CreatePullPointSubscription>
		<wsnt:InitialTerminationTime>PT10M</wsnt:InitialTerminationTime>
	</wsnt:CreatePullPointSubscription>`

	resp, err := c.sendRequest(eventsURL, "http://docs.oasis-open.org/wsn/b-2/CreatePullPointSubscription", body, true)
	if err != nil {
		return nil, err
	}

	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			CreatePullPointSubscriptionResponse struct {
				SubscriptionReference struct {
					Address string `xml:"Address"`
				} `xml:"SubscriptionReference"`
				TerminationTime string `xml:"TerminationTime"`
			} `xml:"CreatePullPointSubscriptionResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	termination, _ := time.Parse(time.RFC3339, response.Body.CreatePullPointSubscriptionResponse.TerminationTime)
	return &PullPointSubscription{
		ReferenceAddress: response.Body.CreatePullPointSubscriptionResponse.SubscriptionReference.Address,
		TerminationTime:  termination,
	}, nil
}

// PullMessages pulls ONVIF events from a subscription endpoint
func (c *Client) PullMessages(subscriptionURL string, timeout time.Duration, limit int) ([]EventMessage, error) {
	if limit <= 0 {
		limit = 1
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	body := fmt.Sprintf(`<wsnt:PullMessages>
		<wsnt:Timeout>%s</wsnt:Timeout>
		<wsnt:MessageLimit>%d</wsnt:MessageLimit>
	</wsnt:PullMessages>`, formatDurationISO8601(timeout), limit)

	resp, err := c.sendRequest(subscriptionURL, "http://docs.oasis-open.org/wsn/b-2/PullMessages", body, true)
	if err != nil {
		return nil, err
	}

	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			PullMessagesResponse struct {
				NotificationMessage []struct {
					Topic struct {
						Value string `xml:",chardata"`
					} `xml:"Topic"`
					Message struct {
						Inner struct {
							UtcTime           string `xml:"UtcTime"`
							PropertyOperation string `xml:"PropertyOperation"`
							Data              struct {
								SimpleItem []eventSimpleItem `xml:"SimpleItem"`
							} `xml:"Data"`
						} `xml:"Message"`
						Raw string `xml:",innerxml"`
					} `xml:"Message"`
				} `xml:"NotificationMessage"`
			} `xml:"PullMessagesResponse"`
		} `xml:"Body"`
	}

	if err := xml.Unmarshal(resp, &response); err != nil {
		return nil, err
	}

	events := make([]EventMessage, 0, len(response.Body.PullMessagesResponse.NotificationMessage))
	for _, msg := range response.Body.PullMessagesResponse.NotificationMessage {
		topic := strings.TrimSpace(msg.Topic.Value)
		if topic == "" {
			topic = "onvif/event"
		}

		text := buildEventMessageText(msg.Message.Inner.PropertyOperation, msg.Message.Inner.Data.SimpleItem, msg.Message.Raw)
		utcTime, _ := time.Parse(time.RFC3339, msg.Message.Inner.UtcTime)

		events = append(events, EventMessage{
			Topic:   topic,
			Message: text,
			UtcTime: utcTime,
		})
	}

	return events, nil
}

type eventSimpleItem struct {
	Name  string `xml:"Name,attr"`
	Value string `xml:"Value,attr"`
}

func buildEventMessageText(operation string, items []eventSimpleItem, rawXML string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", item.Name, item.Value))
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	if strings.TrimSpace(operation) != "" {
		return strings.TrimSpace(operation)
	}
	trimmed := strings.TrimSpace(stripXMLTags(rawXML))
	if trimmed != "" {
		return trimmed
	}
	return "Camera event triggered"
}

func stripXMLTags(input string) string {
	var output strings.Builder
	skip := false
	for _, r := range input {
		switch r {
		case '<':
			skip = true
		case '>':
			skip = false
		default:
			if !skip {
				output.WriteRune(r)
			}
		}
	}
	return output.String()
}

func formatDurationISO8601(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("PT%dS", seconds)
}

// ValidateEndpoint checks if the endpoint is a valid ONVIF device
func ValidateEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("endpoint cannot be empty")
	}

	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return errors.New("endpoint must start with http:// or https://")
	}

	return nil
}
