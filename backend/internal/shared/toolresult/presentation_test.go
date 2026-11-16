package toolresult

import "testing"

func TestBuildPresentationExtractsSearchSourceMetadata(t *testing.T) {
	raw := `{
		"kind": "search",
		"queries": ["iceland population"],
		"sources": [
			{
				"url": "https://www.huaon.com/channel/globaldata/1112834.html",
				"title": "冰岛人口分析",
				"snippet": "2024年冰岛人口总数为40.46万人",
				"site_name": "华经情报网",
				"site_icon": "https://cdn.deepseek.com/site-icons/huaon.com",
				"published_at": "2025-10-15T16:00:00+00:00",
				"image_url": "https://img.example.com/cover.png"
			}
		]
	}`
	presentation := BuildPresentation(raw)
	if presentation == nil {
		t.Fatal("expected presentation")
	}
	if len(presentation.Sources) != 1 {
		t.Fatalf("expected one source, got %#v", presentation.Sources)
	}
	source := presentation.Sources[0]
	if source.URL != "https://www.huaon.com/channel/globaldata/1112834.html" ||
		source.Title != "冰岛人口分析" ||
		source.SiteName != "华经情报网" ||
		source.SiteIcon != "https://cdn.deepseek.com/site-icons/huaon.com" ||
		source.PublishedAt != "2025-10-15T16:00:00+00:00" ||
		source.ImageURL != "https://img.example.com/cover.png" {
		t.Fatalf("unexpected source metadata: %#v", source)
	}
	if source.Kind != "search" {
		t.Fatalf("expected root kind to propagate to sources, got %q", source.Kind)
	}
	if source.Snippet == "" {
		t.Fatalf("expected snippet to be kept, got %#v", source)
	}
}

func TestBuildPresentationExtractsReadLinkPage(t *testing.T) {
	raw := `{"kind":"read_link","page":{"url":"https://example.com/a","title":"Example Page","published_at":"2026-01-01"}}`
	presentation := BuildPresentation(raw)
	if presentation == nil || len(presentation.Sources) != 1 {
		t.Fatalf("expected one read link source, got %#v", presentation)
	}
	source := presentation.Sources[0]
	if source.URL != "https://example.com/a" || source.Title != "Example Page" || source.PublishedAt != "2026-01-01" {
		t.Fatalf("unexpected read link source: %#v", source)
	}
	if source.Kind != "read_link" {
		t.Fatalf("expected read_link kind, got %q", source.Kind)
	}
}

func TestBuildPresentationExtractsWebFetchResultShape(t *testing.T) {
	raw := `{"kind":"deep_search","content":{"type":"web_fetch_result","url":"https://en.tokyotower.co.jp/company/","content":{"type":"document","title":"Tokyo Tower is 333 meters tall."},"published_at":"2026-02-02T00:00:00Z"}}`
	presentation := BuildPresentation(raw)
	if presentation == nil || len(presentation.Sources) != 1 {
		t.Fatalf("expected one page source, got %#v", presentation)
	}
	source := presentation.Sources[0]
	if source.URL != "https://en.tokyotower.co.jp/company/" {
		t.Fatalf("unexpected url: %#v", source)
	}
	if source.Title != "Tokyo Tower is 333 meters tall." {
		t.Fatalf("expected nested document title, got %#v", source)
	}
	if source.PublishedAt != "2026-02-02T00:00:00Z" {
		t.Fatalf("expected published_at, got %#v", source)
	}
	if source.Kind != "page" && source.Kind != "read_link" && source.Kind != "search" {
		// deep_search 归类为 search，页面打开归 page；这里只要求是已知类型。
		t.Fatalf("unexpected source kind: %q", source.Kind)
	}
}

func TestBuildPresentationDropsUnsafeAssetURLs(t *testing.T) {
	raw := `{"kind":"search","sources":[{"url":"https://a.example/x","title":"t","site_icon":"javascript:alert(1)","image_url":"data:image/png;base64,AAAA"}]}`
	presentation := BuildPresentation(raw)
	if presentation == nil || len(presentation.Sources) != 1 {
		t.Fatalf("expected one source, got %#v", presentation)
	}
	source := presentation.Sources[0]
	if source.SiteIcon != "" {
		t.Fatalf("expected unsafe site icon to be dropped, got %q", source.SiteIcon)
	}
	if source.ImageURL != "" {
		t.Fatalf("expected unsafe image url to be dropped, got %q", source.ImageURL)
	}
}

func TestBuildPresentationCapsSourceCount(t *testing.T) {
	raw := `{"kind":"search","sources":[`
	for index := 0; index < toolTracePresentationMaxSources+4; index++ {
		if index > 0 {
			raw += ","
		}
		raw += `{"url":"https://example.com/` + itoa(index) + `","title":"t"}`
	}
	raw += `]}`
	presentation := BuildPresentation(raw)
	if presentation == nil {
		t.Fatal("expected presentation")
	}
	if len(presentation.Sources) != toolTracePresentationMaxSources {
		t.Fatalf("expected source cap %d, got %d", toolTracePresentationMaxSources, len(presentation.Sources))
	}
}

func TestBuildPresentationToleratesErrorOnlyPayload(t *testing.T) {
	// 失败结果由 ToolCall.ErrorJSON 承载错误码，展示层允许没有可展示内容。
	if presentation := BuildPresentation(`{"kind":"read_link","error_code":"url_not_accessible"}`); presentation != nil && presentation.Text == "" && len(presentation.Sources) == 0 {
		t.Fatalf("expected nil or non-empty presentation, got %#v", presentation)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
