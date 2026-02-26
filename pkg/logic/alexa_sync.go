package logic

import (
	"fmt"
	"time"
)

// AlexaEventFactory generates Alexa-compliant JSON event structures.
// The relay service handles Bearer token / scope injection.
type AlexaEventFactory struct{}

func NewAlexaEventFactory() *AlexaEventFactory {
	return &AlexaEventFactory{}
}

func (f *AlexaEventFactory) CreateDeleteReport(endpointID string) map[string]any {
	return map[string]any{
		"event": map[string]any{
			"header": map[string]any{
				"namespace":      "Alexa.Discovery",
				"name":           "DeleteReport",
				"messageId":      f.newMsgID(),
				"payloadVersion": "3",
			},
			"payload": map[string]any{
				"endpoints": []map[string]any{
					{"endpointId": endpointID},
				},
			},
		},
	}
}

func (f *AlexaEventFactory) CreateAddOrUpdateReport(endpoint map[string]any) map[string]any {
	return map[string]any{
		"event": map[string]any{
			"header": map[string]any{
				"namespace":      "Alexa.Discovery",
				"name":           "AddOrUpdateReport",
				"messageId":      f.newMsgID(),
				"payloadVersion": "3",
			},
			"payload": map[string]any{
				"endpoints": []map[string]any{endpoint},
			},
		},
	}
}

func (f *AlexaEventFactory) newMsgID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
