package httpapi_test

import (
	"testing"
)

func newTag(t *testing.T, c *client, name string) string {
	t.Helper()
	r := c.call("POST", "/api/v1/tags", map[string]any{"name": name})
	if r.Status != 201 {
		t.Fatalf("create tag %q: %d %v", name, r.Status, r.Body)
	}
	return r.Body["tag"].(map[string]any)["id"].(string)
}

func tagNames(m map[string]any) []string {
	out := []string{}
	for _, t := range m["tags"].([]any) {
		out = append(out, t.(map[string]any)["name"].(string))
	}
	return out
}

func listIDs(t *testing.T, c *client, query string) []string {
	t.Helper()
	r := c.call("GET", "/api/v1/monitors"+query, nil)
	if r.Status != 200 {
		t.Fatalf("list %s: %d %v", query, r.Status, r.Body)
	}
	out := []string{}
	for _, m := range r.Body["monitors"].([]any) {
		out = append(out, m.(map[string]any)["id"].(string))
	}
	return out
}

func TestTagCRUDOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "tags@example.com")

	r := c.call("POST", "/api/v1/tags", map[string]any{"name": "  Acme Corp ", "color": "#1A73E8"})
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	tag := r.Body["tag"].(map[string]any)
	id := tag["id"].(string)
	if tag["name"] != "Acme Corp" || tag["color"] != "#1a73e8" || tag["monitor_count"].(float64) != 0 {
		t.Fatalf("created tag: %v", tag)
	}

	// Names are unique per organization regardless of case.
	if r := c.call("POST", "/api/v1/tags", map[string]any{"name": "acme corp"}); r.Status != 409 || errCode(r) != "TAG_EXISTS" {
		t.Fatalf("duplicate: %d %v", r.Status, r.Body)
	}
	for _, body := range []map[string]any{{"name": ""}, {"name": "x", "color": "red"}, {"name": string(make([]byte, 41))}} {
		if r := c.call("POST", "/api/v1/tags", body); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Fatalf("invalid %v: %d %v", body, r.Status, r.Body)
		}
	}

	r = c.call("PATCH", "/api/v1/tags/"+id, map[string]any{"name": "Acme", "color": nil})
	if r.Status != 200 || r.Body["tag"].(map[string]any)["name"] != "Acme" || r.Body["tag"].(map[string]any)["color"] != nil {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	other := newTag(t, c, "Beta")
	if r := c.call("PATCH", "/api/v1/tags/"+other, map[string]any{"name": "ACME"}); r.Status != 409 {
		t.Fatalf("rename onto an existing name: %d %v", r.Status, r.Body)
	}

	list := c.call("GET", "/api/v1/tags", nil).Body["tags"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["name"] != "Acme" || list[1].(map[string]any)["name"] != "Beta" {
		t.Fatalf("list: %v", list)
	}

	// Deleting a tag keeps its monitors.
	m := monitorBody("Site")
	m["tag_ids"] = []string{id}
	mid := c.call("POST", "/api/v1/monitors", m).Body["monitor"].(map[string]any)["id"].(string)
	if r := c.call("DELETE", "/api/v1/tags/"+id, nil); r.Status != 204 {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	got := c.call("GET", "/api/v1/monitors/"+mid, nil)
	if got.Status != 200 || len(tagNames(got.Body["monitor"].(map[string]any))) != 0 {
		t.Fatalf("monitor after its tag was deleted: %d %v", got.Status, got.Body)
	}
	if r := c.call("DELETE", "/api/v1/tags/"+id, nil); r.Status != 404 {
		t.Fatalf("delete again: %d", r.Status)
	}
	if r := c.call("PATCH", "/api/v1/tags/not-a-uuid", map[string]any{"name": "x"}); r.Status != 404 {
		t.Fatalf("bad id: %d", r.Status)
	}
}

func TestMonitorTagsAndFilterOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "filter@example.com")
	acme, beta := newTag(t, c, "acme"), newTag(t, c, "Beta")

	body := monitorBody("Shop")
	body["tag_ids"] = []string{beta, acme, acme}
	r := c.call("POST", "/api/v1/monitors", body)
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	shop := r.Body["monitor"].(map[string]any)
	if n := tagNames(shop); len(n) != 2 || n[0] != "acme" || n[1] != "Beta" {
		t.Fatalf("tags on create (sorted, without repeats): %v", n)
	}
	blog := c.call("POST", "/api/v1/monitors", monitorBody("Blog")).Body["monitor"].(map[string]any)
	if n := tagNames(blog); len(n) != 0 {
		t.Fatalf("untagged monitor: %v", n)
	}

	if ids := listIDs(t, c, "?tag="+acme); len(ids) != 1 || ids[0] != shop["id"] {
		t.Fatalf("filter by acme: %v", ids)
	}
	if r := c.call("GET", "/api/v1/monitors?tag=nope", nil); r.Status != 400 {
		t.Fatalf("bad tag filter: %d", r.Status)
	}

	// PATCH replaces the set; leaving tag_ids out keeps it.
	r = c.call("PATCH", "/api/v1/monitors/"+blog["id"].(string), map[string]any{"tag_ids": []string{acme}})
	if r.Status != 200 || len(tagNames(r.Body["monitor"].(map[string]any))) != 1 {
		t.Fatalf("patch tags: %d %v", r.Status, r.Body)
	}
	r = c.call("PATCH", "/api/v1/monitors/"+blog["id"].(string), map[string]any{"name": "Blog 2"})
	if n := tagNames(r.Body["monitor"].(map[string]any)); len(n) != 1 || n[0] != "acme" {
		t.Fatalf("unrelated patch changed tags: %v", n)
	}
	r = c.call("PATCH", "/api/v1/monitors/"+shop["id"].(string), map[string]any{"tag_ids": []string{}})
	if len(tagNames(r.Body["monitor"].(map[string]any))) != 0 {
		t.Fatalf("clearing tags: %v", r.Body)
	}
	if ids := listIDs(t, c, "?tag="+acme); len(ids) != 1 || ids[0] != blog["id"] {
		t.Fatalf("filter after retagging: %v", ids)
	}
	if n := c.call("GET", "/api/v1/tags", nil).Body["tags"].([]any)[0].(map[string]any)["monitor_count"]; n.(float64) != 1 {
		t.Fatalf("monitor_count: %v", n)
	}

	// At most 10 tags per monitor.
	many := []string{}
	for i := range 11 {
		many = append(many, newTag(t, c, "t"+string(rune('a'+i))))
	}
	if r := c.call("PATCH", "/api/v1/monitors/"+shop["id"].(string), map[string]any{"tag_ids": many}); r.Status != 400 {
		t.Fatalf("11 tags: %d %v", r.Status, r.Body)
	}

	// The account export lists tag names.
	exp := c.call("GET", "/api/v1/account/export", nil)
	if exp.Status != 200 {
		t.Fatalf("export: %d %v", exp.Status, exp.Body)
	}
	mons := exp.Body["organizations"].([]any)[0].(map[string]any)["monitors"].([]any)
	found := false
	for _, m := range mons {
		if tags := m.(map[string]any)["tags"].([]any); len(tags) == 1 && tags[0] == "acme" {
			found = true
		}
	}
	if !found {
		t.Fatalf("export without tags: %v", mons)
	}
}

func TestTagsAreTenantIsolated(t *testing.T) {
	e := newEnv(t, 1000)
	alice, _ := e.register(t, "alice-tags@example.com")
	bob, _ := e.register(t, "bob-tags@example.com")
	aliceTag := newTag(t, alice, "Alice client")
	aliceMon := alice.call("POST", "/api/v1/monitors", monitorBody("Alice site")).Body["monitor"].(map[string]any)["id"].(string)
	bobMon := bob.call("POST", "/api/v1/monitors", monitorBody("Bob site")).Body["monitor"].(map[string]any)["id"].(string)

	if l := bob.call("GET", "/api/v1/tags", nil).Body["tags"].([]any); len(l) != 0 {
		t.Fatalf("bob sees alice's tags: %v", l)
	}
	if r := bob.call("PATCH", "/api/v1/tags/"+aliceTag, map[string]any{"name": "pwned"}); r.Status != 404 {
		t.Fatalf("bob renamed alice's tag: %d", r.Status)
	}
	if r := bob.call("DELETE", "/api/v1/tags/"+aliceTag, nil); r.Status != 404 {
		t.Fatalf("bob deleted alice's tag: %d", r.Status)
	}
	// Bob cannot attach Alice's tag to his own monitor, directly or in bulk.
	if r := bob.call("PATCH", "/api/v1/monitors/"+bobMon, map[string]any{"tag_ids": []string{aliceTag}}); r.Status != 400 {
		t.Fatalf("foreign tag on patch: %d %v", r.Status, r.Body)
	}
	body := monitorBody("Bob 2")
	body["tag_ids"] = []string{aliceTag}
	if r := bob.call("POST", "/api/v1/monitors", body); r.Status != 400 {
		t.Fatalf("foreign tag on create: %d %v", r.Status, r.Body)
	}
	if r := bob.call("POST", "/api/v1/monitors/bulk", map[string]any{"monitor_ids": []string{bobMon}, "action": "add_tags", "tag_ids": []string{aliceTag}}); r.Status != 400 {
		t.Fatalf("foreign tag in bulk: %d %v", r.Status, r.Body)
	}
	// A bulk action naming Alice's monitor fails as a whole and changes nothing, not even Bob's own monitor.
	r := bob.call("POST", "/api/v1/monitors/bulk", map[string]any{"monitor_ids": []string{bobMon, aliceMon}, "action": "pause"})
	if r.Status != 404 {
		t.Fatalf("bulk on a foreign monitor: %d %v", r.Status, r.Body)
	}
	if alice.call("GET", "/api/v1/monitors/"+aliceMon, nil).Body["monitor"].(map[string]any)["enabled"] != true ||
		bob.call("GET", "/api/v1/monitors/"+bobMon, nil).Body["monitor"].(map[string]any)["enabled"] != true {
		t.Fatal("a failed bulk action changed a monitor")
	}
	// Filtering by a foreign tag shows nothing.
	if ids := listIDs(t, bob, "?tag="+aliceTag); len(ids) != 0 {
		t.Fatalf("foreign tag filter: %v", ids)
	}
}

func TestBulkMonitorActionsOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "bulk@example.com")
	ids := []string{}
	for _, n := range []string{"a", "b", "c"} {
		ids = append(ids, c.call("POST", "/api/v1/monitors", monitorBody(n)).Body["monitor"].(map[string]any)["id"].(string))
	}
	bulk := func(body map[string]any) resp {
		t.Helper()
		return c.call("POST", "/api/v1/monitors/bulk", body)
	}
	get := func(id string) map[string]any {
		t.Helper()
		return c.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)
	}

	if r := bulk(map[string]any{"monitor_ids": ids[:2], "action": "pause"}); r.Status != 200 || r.Body["updated"].(float64) != 2 {
		t.Fatalf("pause: %d %v", r.Status, r.Body)
	}
	if get(ids[0])["enabled"] != false || get(ids[1])["enabled"] != false || get(ids[2])["enabled"] != true {
		t.Fatal("pause changed the wrong monitors")
	}
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "resume"}); r.Status != 200 {
		t.Fatalf("resume: %d %v", r.Status, r.Body)
	}
	if get(ids[0])["enabled"] != true {
		t.Fatal("resume did not resume")
	}

	// Free allows 5-minute checks at the fastest; a faster bulk interval is a plan limit, and nothing changes.
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "set_interval", "check_interval_seconds": 60}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("interval below plan: %d %v", r.Status, r.Body)
	}
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "set_interval", "check_interval_seconds": 900}); r.Status != 200 {
		t.Fatalf("set interval: %d %v", r.Status, r.Body)
	}
	if get(ids[2])["check_interval_seconds"].(float64) != 900 {
		t.Fatal("interval not set")
	}
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "set_interval"}); r.Status != 400 {
		t.Fatalf("interval missing: %d", r.Status)
	}
	// A monitor that repeats a state-changing request keeps the slower minimum, even on a plan that allows 1 minute.
	setPlan(t, e, "pro")
	if _, err := e.pool.Exec(t.Context(), `UPDATE monitors SET method = 'POST' WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "set_interval", "check_interval_seconds": 60}); r.Status != 400 {
		t.Fatalf("write method below 5 minutes: %d %v", r.Status, r.Body)
	}
	if r := bulk(map[string]any{"monitor_ids": ids[1:], "action": "set_interval", "check_interval_seconds": 60}); r.Status != 200 {
		t.Fatalf("1-minute interval on pro: %d %v", r.Status, r.Body)
	}

	acme, beta := newTag(t, c, "Acme"), newTag(t, c, "Beta")
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "add_tags", "tag_ids": []string{acme, beta}}); r.Status != 200 {
		t.Fatalf("add tags: %d %v", r.Status, r.Body)
	}
	// Adding again is harmless.
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "add_tags", "tag_ids": []string{acme}}); r.Status != 200 {
		t.Fatalf("add tags again: %d %v", r.Status, r.Body)
	}
	if n := tagNames(get(ids[1])); len(n) != 2 {
		t.Fatalf("after add: %v", n)
	}
	if r := bulk(map[string]any{"monitor_ids": ids[:1], "action": "remove_tags", "tag_ids": []string{beta}}); r.Status != 200 {
		t.Fatalf("remove tags: %d %v", r.Status, r.Body)
	}
	if n := tagNames(get(ids[0])); len(n) != 1 || n[0] != "Acme" {
		t.Fatalf("after remove: %v", n)
	}

	for name, body := range map[string]map[string]any{
		"no monitors":    {"monitor_ids": []string{}, "action": "pause"},
		"unknown action": {"monitor_ids": ids, "action": "explode"},
		"no tags":        {"monitor_ids": ids, "action": "add_tags"},
	} {
		if r := bulk(body); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}

	if r := bulk(map[string]any{"monitor_ids": ids[:2], "action": "delete"}); r.Status != 200 || r.Body["updated"].(float64) != 2 {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	if left := listIDs(t, c, ""); len(left) != 1 || left[0] != ids[2] {
		t.Fatalf("after bulk delete: %v", left)
	}
	if n := e.countRows(t, `SELECT count(*) FROM monitor_tags WHERE monitor_id = ANY($1::uuid[])`, ids[:2]); n != 0 {
		t.Fatalf("deleted monitors kept %d tag links", n)
	}
	// A deleted monitor cannot be acted on again.
	if r := bulk(map[string]any{"monitor_ids": ids, "action": "resume"}); r.Status != 404 {
		t.Fatalf("bulk on deleted monitors: %d %v", r.Status, r.Body)
	}
}
