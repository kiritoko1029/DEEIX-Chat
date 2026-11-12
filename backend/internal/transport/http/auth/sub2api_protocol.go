package auth

import appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"

type Sub2APIProtocolChoiceRequest struct {
	KeyID    int64  `json:"keyID" minimum:"1" maximum:"9007199254740991"`
	Protocol string `json:"protocol" enums:"openai_responses,anthropic_messages,openai_chat_completions"`
}
type Sub2APIProtocolItemResponse struct {
	KeyID     int64  `json:"keyID"`
	Name      string `json:"name"`
	GroupName string `json:"groupName"`
	Platform  string `json:"platform"`
}
type Sub2APIProtocolSelectionResponse struct {
	Items     []Sub2APIProtocolItemResponse `json:"items"`
	Protocols []string                      `json:"protocols"`
}

func (request Sub2APIExchangeRequest) options() appauth.Sub2APIExchangeOptions {
	result := appauth.Sub2APIExchangeOptions{SkipKeyIDs: request.SkipKeyIDs, ProtocolSelections: make([]appauth.Sub2APIProtocolChoice, len(request.ProtocolSelections))}
	for i, choice := range request.ProtocolSelections {
		result.ProtocolSelections[i] = appauth.Sub2APIProtocolChoice{KeyID: choice.KeyID, Protocol: choice.Protocol}
	}
	return result
}
func toSub2APIProtocolSelectionResponse(selection appauth.Sub2APIProtocolSelection) Sub2APIProtocolSelectionResponse {
	result := Sub2APIProtocolSelectionResponse{Items: make([]Sub2APIProtocolItemResponse, len(selection.Items)), Protocols: selection.Protocols}
	for i, item := range selection.Items {
		result.Items[i] = Sub2APIProtocolItemResponse{KeyID: item.KeyID, Name: item.Name, GroupName: item.GroupName, Platform: item.Platform}
	}
	return result
}
