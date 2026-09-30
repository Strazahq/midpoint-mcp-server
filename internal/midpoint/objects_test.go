package midpoint

import (
	"encoding/json"
	"testing"
)

func TestParseObjectList(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantN   int
		wantErr bool
	}{
		{
			// The real midPoint 4.10 shape: ObjectListType wrapped under the
			// top-level "object", results under its own nested "object".
			name:  "nested list (real shape)",
			body:  `{"@ns":"x","object":{"@type":"...ObjectListType","object":[{"oid":"a"},{"oid":"b"}]}}`,
			wantN: 2,
		},
		{
			name:  "nested single object collapsed",
			body:  `{"object":{"@type":"...ObjectListType","object":{"oid":"a"}}}`,
			wantN: 1,
		},
		{
			name:  "flat array (tolerated)",
			body:  `{"object":[{"oid":"a"},{"oid":"b"},{"oid":"c"}]}`,
			wantN: 3,
		},
		{
			name:  "empty list",
			body:  `{"object":{"@type":"...ObjectListType"}}`,
			wantN: 0,
		},
		{
			name:  "no object key",
			body:  `{"@ns":"x"}`,
			wantN: 0,
		},
		{
			name:  "null object",
			body:  `{"object":null}`,
			wantN: 0,
		},
		{
			name:    "malformed",
			body:    `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseObjectList([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseObjectList: %v", err)
			}
			if len(got) != tt.wantN {
				t.Fatalf("got %d objects, want %d", len(got), tt.wantN)
			}
			// Every extracted element must carry its real fields (the regression:
			// the wrapper used to be decoded in place, yielding empty oids).
			for i, raw := range got {
				var o struct {
					OID string `json:"oid"`
				}
				if err := json.Unmarshal(raw, &o); err != nil {
					t.Fatalf("element %d: %v", i, err)
				}
				if o.OID == "" {
					t.Errorf("element %d has empty oid: %s", i, raw)
				}
			}
		})
	}
}

// midPoint spells a reference's keys two ways: plain in a single-object GET,
// namespace-prefixed ("t:oid") in a search result. Both must decode.
func TestRefJSONBothSpellings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want refJSON
	}{
		{
			name: "plain keys (single-object GET)",
			body: `{"oid":"u-1","type":"c:UserType","relation":"org:default","targetName":"alice"}`,
			want: refJSON{OID: "u-1", Type: "c:UserType", Relation: "org:default", TargetName: polyString{"alice"}},
		},
		{
			// The shape midPoint 4.10.3 returns inside POST /{collection}/search.
			name: "prefixed keys (search result)",
			body: `{"t:oid":"u-1","t:relation":"org:default","t:type":"c:UserType","targetName":"alice"}`,
			want: refJSON{OID: "u-1", Type: "c:UserType", Relation: "org:default", TargetName: polyString{"alice"}},
		},
		{
			name: "prefixed keys, polystring name, no relation",
			body: `{"t:oid":"r-1","t:type":"c:RoleType","targetName":{"orig":"Superuser","norm":"superuser"}}`,
			want: refJSON{OID: "r-1", Type: "c:RoleType", TargetName: polyString{"Superuser"}},
		},
		{
			name: "both spellings: the plain key wins",
			body: `{"t:oid":"prefixed","oid":"plain"}`,
			want: refJSON{OID: "plain"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got refJSON
			if err := json.Unmarshal([]byte(tt.body), &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}

	var bad refJSON
	if err := json.Unmarshal([]byte(`{"t:oid":42}`), &bad); err == nil {
		t.Error("a non-string oid decoded without error")
	}
}

// Every decoder that reads references goes through refJSON, so the prefixed
// spelling reaches parentOrgRef and assignment targets too.
func TestUserRefsPrefixedKeys(t *testing.T) {
	var u userJSON
	body := `{"oid":"u-1","name":"alice",
		"parentOrgRef":[
			{"t:oid":"o-1","t:relation":"org:manager","t:type":"c:OrgType","targetName":"dev-ops"},
			{"t:oid":"o-2","t:relation":"org:default","t:type":"c:OrgType"}],
		"assignment":{"@id":3,"targetRef":{"t:oid":"o-3","t:relation":"org:default","t:type":"c:OrgType"}}}`
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	orgs := u.parentOrgs()
	if len(orgs) != 2 || orgs[0].OID != "o-1" || !orgs[0].isManagerOf("manager") || orgs[0].TargetName.value() != "dev-ops" ||
		orgs[1].OID != "o-2" || orgs[1].source != OrgSourceParentOrgRef {
		t.Errorf("parentOrgs = %+v", orgs)
	}
	if assigned := u.assignedOrgs(); len(assigned) != 1 || assigned[0].OID != "o-3" {
		t.Errorf("assignedOrgs = %+v, want o-3", assigned)
	}
}
