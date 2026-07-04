package msgintent

import "testing"

func TestClassifyMatrix(t *testing.T) {
	dm := func(text string) MessageSignals {
		return MessageSignals{Text: text, PeerKind: "direct"}
	}
	group := func(text string) MessageSignals {
		return MessageSignals{Text: text, PeerKind: "group"}
	}
	groupMentioned := func(text string) MessageSignals {
		return MessageSignals{Text: text, PeerKind: "group", WasMentioned: true}
	}

	cases := []struct {
		name       string
		signals    MessageSignals
		wantIntent Intent
		wantURL    string
	}{
		// --- shortlink_offer: platform URL in any context ---
		{"shopee URL in DM", dm("https://shopee.vn/product-abc-123"), IntentShortlinkOffer, "https://shopee.vn/product-abc-123"},
		{"shopee short URL group no mention", group("xem cái này https://s.shopee.vn/abc"), IntentShortlinkOffer, "https://s.shopee.vn/abc"},
		{"shp.ee link", group("https://shp.ee/xyz9"), IntentShortlinkOffer, "https://shp.ee/xyz9"},
		{"lazada URL", dm("https://lazada.vn/products/item-i123.html"), IntentShortlinkOffer, "https://lazada.vn/products/item-i123.html"},
		{"lazada short URL", group("mua đi https://s.lazada.vn/s.abc"), IntentShortlinkOffer, "https://s.lazada.vn/s.abc"},
		{"tiktok URL", dm("https://tiktok.com/@shop/video/123"), IntentShortlinkOffer, "https://tiktok.com/@shop/video/123"},
		{"tiktok vt short URL", group("https://vt.tiktok.com/ZSabc/"), IntentShortlinkOffer, "https://vt.tiktok.com/ZSabc/"},
		{"URL between two people chatting", group("A ơi mua cái này nè https://shopee.vn/abc đẹp lắm"), IntentShortlinkOffer, "https://shopee.vn/abc"},
		{"URL with trailing punctuation", dm("https://shopee.vn/abc."), IntentShortlinkOffer, "https://shopee.vn/abc"},
		{"URL with subdomain", dm("https://vn.shp.ee/deal"), IntentShortlinkOffer, "https://vn.shp.ee/deal"},
		{"pre-extracted URL no text match", MessageSignals{Text: "check this", URLs: []string{"https://shopee.vn/x"}, PeerKind: "group"}, IntentShortlinkOffer, "https://shopee.vn/x"},
		{"multiple URLs picks first platform", group("https://example.com/a https://lazada.vn/b"), IntentShortlinkOffer, "https://lazada.vn/b"},

		// --- commission_lookup: URL + commission keyword ---
		{"URL + hoa hồng", dm("link này hoa hồng bao nhiêu https://shopee.vn/abc"), IntentCommissionLookup, "https://shopee.vn/abc"},
		{"URL + commission en", dm("what's the commission for https://lazada.vn/x"), IntentCommissionLookup, "https://lazada.vn/x"},
		{"URL + chiết khấu", groupMentioned("chiết khấu link này nhiêu https://shopee.vn/z"), IntentCommissionLookup, "https://shopee.vn/z"},
		{"URL + percent sign", dm("https://tiktok.com/@a/video/1 được mấy %"), IntentCommissionLookup, "https://tiktok.com/@a/video/1"},

		// --- commission_broadcast: URL + admin broadcast keyword ---
		{"URL + gửi bảng kê", dm("gửi bảng kê link https://shopee.vn/abc cho nhóm"), IntentCommissionBroadcast, "https://shopee.vn/abc"},
		{"URL + gửi thống kê", dm("gửi thống kê https://lazada.vn/y"), IntentCommissionBroadcast, "https://lazada.vn/y"},

		// --- off_scope_question: question addressed to bot, no business match ---
		{"question mentioned in group", groupMentioned("bot ơi mai trời có mưa không?"), IntentOffScopeQuestion, ""},
		{"question in DM", dm("mai trời mưa không?"), IntentOffScopeQuestion, ""},
		{"vi interrogative no question mark DM", dm("làm sao để đổi mật khẩu"), IntentOffScopeQuestion, ""},
		{"en question mentioned", groupMentioned("what time is it"), IntentOffScopeQuestion, ""},
		{"reply to bot question", MessageSignals{Text: "cái này dùng thế nào", PeerKind: "group", IsReplyToBot: true}, IntentOffScopeQuestion, ""},
		{"fullwidth question mark DM", dm("是真的吗？"), IntentOffScopeQuestion, ""},

		// --- chatter: not the bot's business ---
		{"question between two people", group("B ơi cái áo này đẹp không?"), IntentChatter, ""},
		{"plain chatter group", group("hôm nay đi ăn gì nhỉ hehe"), IntentChatter, ""},
		{"greeting no mention", group("chào cả nhà"), IntentChatter, ""},
		{"non-platform URL not addressed", group("xem youtube nè https://youtube.com/watch?v=x"), IntentChatter, ""},
		{"sticker-ish short text", group("haha"), IntentChatter, ""},
		{"en smalltalk word-boundary safe", group("nice showroom bro"), IntentChatter, ""},

		// --- general: addressed but nothing specific ---
		{"DM statement no question", dm("tôi muốn đăng ký làm cộng tác viên"), IntentGeneral, ""},
		{"mentioned statement", groupMentioned("bot ghi nhận thông tin này nhé"), IntentGeneral, ""},
		{"mentioned request keyword is question", groupMentioned("bot check giúp đơn hôm qua nhé"), IntentOffScopeQuestion, ""},
		{"non-platform URL in DM", dm("https://youtube.com/watch?v=abc"), IntentGeneral, ""},

		// --- edge cases ---
		{"URL takes priority over question", dm("mua cái này ở đâu https://shopee.vn/abc?"), IntentShortlinkOffer, "https://shopee.vn/abc"},
		{"empty text group", group(""), IntentChatter, ""},
		{"empty text DM", dm(""), IntentGeneral, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.signals, nil)
			if got.Intent != tc.wantIntent {
				t.Fatalf("intent = %q, want %q (decision: %+v)", got.Intent, tc.wantIntent, got)
			}
			if tc.wantURL != "" && got.MatchedURL != tc.wantURL {
				t.Fatalf("matchedURL = %q, want %q", got.MatchedURL, tc.wantURL)
			}
		})
	}
}

func TestClassifyKeywordOverrides(t *testing.T) {
	overrides := &KeywordOverrides{CommissionKeywords: []string{"hoa hong ck"}}
	got := Classify(MessageSignals{
		Text:     "hoa hong ck link này https://shopee.vn/abc",
		PeerKind: "direct",
	}, overrides)
	if got.Intent != IntentCommissionLookup {
		t.Fatalf("override keyword not applied, got %q", got.Intent)
	}
	// defaults must still work alongside overrides
	got = Classify(MessageSignals{
		Text:     "hoa hồng link này https://shopee.vn/abc",
		PeerKind: "direct",
	}, overrides)
	if got.Intent != IntentCommissionLookup {
		t.Fatalf("default keyword lost after override, got %q", got.Intent)
	}
}

func TestClassifyQuestionFlagsPropagate(t *testing.T) {
	got := Classify(MessageSignals{Text: "sao vậy?", PeerKind: "group", WasMentioned: true}, nil)
	if !got.IsQuestion || !got.AddressedToBot {
		t.Fatalf("flags not propagated: %+v", got)
	}
	got = Classify(MessageSignals{Text: "hello mọi người", PeerKind: "group"}, nil)
	if got.IsQuestion || got.AddressedToBot {
		t.Fatalf("flags should be false: %+v", got)
	}
}

func TestDetectPlatformURLRejectsNonWhitelist(t *testing.T) {
	for _, u := range []string{
		"https://evil.com/shopee.vn/phish",
		"https://shopee.vn.evil.com/x",
		"https://notshopee.vn/x",
		"javascript:alert(1)",
	} {
		if matched, _ := DetectPlatformURL(u, nil); matched != "" {
			t.Fatalf("should not match %q, got %q", u, matched)
		}
	}
}
