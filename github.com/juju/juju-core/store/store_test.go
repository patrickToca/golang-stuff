// Copyright 2011, 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package store_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
	"sync"
	stdtesting "testing"
	"time"

	"labix.org/v2/mgo/bson"
	. "launchpad.net/gocheck"

	"launchpad.net/juju-core/charm"
	"launchpad.net/juju-core/store"
	"launchpad.net/juju-core/testing"
)

func Test(t *stdtesting.T) {
	TestingT(t)
}

var _ = Suite(&StoreSuite{})
var _ = Suite(&TrivialSuite{})

type StoreSuite struct {
	MgoSuite
	testing.HTTPSuite
	testing.LoggingSuite
	store *store.Store
}

type TrivialSuite struct{}

func (s *StoreSuite) SetUpSuite(c *C) {
	s.MgoSuite.SetUpSuite(c)
	s.HTTPSuite.SetUpSuite(c)
	s.LoggingSuite.SetUpSuite(c)
}

func (s *StoreSuite) TearDownSuite(c *C) {
	s.LoggingSuite.TearDownSuite(c)
	s.HTTPSuite.TearDownSuite(c)
	s.MgoSuite.TearDownSuite(c)
}

func (s *StoreSuite) SetUpTest(c *C) {
	s.MgoSuite.SetUpTest(c)
	s.LoggingSuite.SetUpTest(c)
	var err error
	s.store, err = store.Open(s.Addr)
	c.Assert(err, IsNil)
}

func (s *StoreSuite) TearDownTest(c *C) {
	s.LoggingSuite.TearDownTest(c)
	s.HTTPSuite.TearDownTest(c)
	if s.store != nil {
		s.store.Close()
	}
	s.MgoSuite.TearDownTest(c)
}

// FakeCharmDir is a charm that implements the interface that the
// store publisher cares about.
type FakeCharmDir struct {
	revision interface{} // so we can tell if it's not set.
	error    string
}

func (d *FakeCharmDir) Meta() *charm.Meta {
	return &charm.Meta{
		Name:        "fakecharm",
		Summary:     "Fake charm for testing purposes.",
		Description: "This is a fake charm for testing purposes.\n",
		Provides:    make(map[string]charm.Relation),
		Requires:    make(map[string]charm.Relation),
		Peers:       make(map[string]charm.Relation),
	}
}

func (d *FakeCharmDir) Config() *charm.Config {
	return &charm.Config{make(map[string]charm.Option)}
}

func (d *FakeCharmDir) SetRevision(revision int) {
	d.revision = revision
}

func (d *FakeCharmDir) BundleTo(w io.Writer) error {
	if d.error == "beforeWrite" {
		return fmt.Errorf(d.error)
	}
	_, err := w.Write([]byte(fmt.Sprintf("charm-revision-%v", d.revision)))
	if d.error == "afterWrite" {
		return fmt.Errorf(d.error)
	}
	return err
}

func (s *StoreSuite) TestCharmPublisherWithRevisionedURL(c *C) {
	urls := []*charm.URL{charm.MustParseURL("cs:oneiric/wordpress-0")}
	pub, err := s.store.CharmPublisher(urls, "some-digest")
	c.Assert(err, ErrorMatches, "CharmPublisher: got charm URL with revision: cs:oneiric/wordpress-0")
	c.Assert(pub, IsNil)
}

func (s *StoreSuite) TestCharmPublisher(c *C) {
	urlA := charm.MustParseURL("cs:oneiric/wordpress-a")
	urlB := charm.MustParseURL("cs:oneiric/wordpress-b")
	urls := []*charm.URL{urlA, urlB}

	pub, err := s.store.CharmPublisher(urls, "some-digest")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 0)

	err = pub.Publish(testing.Charms.ClonedDir(c.MkDir(), "dummy"))
	c.Assert(err, IsNil)

	for _, url := range urls {
		info, rc, err := s.store.OpenCharm(url)
		c.Assert(err, IsNil)
		c.Assert(info.Revision(), Equals, 0)
		c.Assert(info.Digest(), Equals, "some-digest")
		data, err := ioutil.ReadAll(rc)
		c.Check(err, IsNil)
		err = rc.Close()
		c.Assert(err, IsNil)
		bundle, err := charm.ReadBundleBytes(data)
		c.Assert(err, IsNil)

		// The same information must be available by reading the
		// full charm data...
		c.Assert(bundle.Meta().Name, Equals, "dummy")
		c.Assert(bundle.Config().Options["title"].Default, Equals, "My Title")

		// ... and the queriable details.
		c.Assert(info.Meta().Name, Equals, "dummy")
		c.Assert(info.Config().Options["title"].Default, Equals, "My Title")

		info2, err := s.store.CharmInfo(url)
		c.Assert(err, IsNil)
		c.Assert(info2, DeepEquals, info)
	}
}

func (s *StoreSuite) TestCharmPublishError(c *C) {
	url := charm.MustParseURL("cs:oneiric/wordpress")
	urls := []*charm.URL{url}

	// Publish one successfully to bump the revision so we can
	// make sure it is being correctly set below.
	pub, err := s.store.CharmPublisher(urls, "one-digest")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 0)
	err = pub.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)

	pub, err = s.store.CharmPublisher(urls, "another-digest")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 1)
	err = pub.Publish(&FakeCharmDir{error: "beforeWrite"})
	c.Assert(err, ErrorMatches, "beforeWrite")

	pub, err = s.store.CharmPublisher(urls, "another-digest")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 1)
	err = pub.Publish(&FakeCharmDir{error: "afterWrite"})
	c.Assert(err, ErrorMatches, "afterWrite")

	// Still at the original charm revision that succeeded first.
	info, err := s.store.CharmInfo(url)
	c.Assert(err, IsNil)
	c.Assert(info.Revision(), Equals, 0)
	c.Assert(info.Digest(), Equals, "one-digest")
}

func (s *StoreSuite) TestCharmInfoNotFound(c *C) {
	info, err := s.store.CharmInfo(charm.MustParseURL("cs:oneiric/wordpress"))
	c.Assert(err, Equals, store.ErrNotFound)
	c.Assert(info, IsNil)
}

func (s *StoreSuite) TestRevisioning(c *C) {
	urlA := charm.MustParseURL("cs:oneiric/wordpress-a")
	urlB := charm.MustParseURL("cs:oneiric/wordpress-b")
	urls := []*charm.URL{urlA, urlB}

	tests := []struct {
		urls []*charm.URL
		data string
	}{
		{urls[0:], "charm-revision-0"},
		{urls[1:], "charm-revision-1"},
		{urls[0:], "charm-revision-2"},
	}

	for i, t := range tests {
		pub, err := s.store.CharmPublisher(t.urls, fmt.Sprintf("digest-%d", i))
		c.Assert(err, IsNil)
		c.Assert(pub.Revision(), Equals, i)

		err = pub.Publish(&FakeCharmDir{})
		c.Assert(err, IsNil)
	}

	for i, t := range tests {
		for _, url := range t.urls {
			url = url.WithRevision(i)
			info, rc, err := s.store.OpenCharm(url)
			c.Assert(err, IsNil)
			data, err := ioutil.ReadAll(rc)
			cerr := rc.Close()
			c.Assert(info.Revision(), Equals, i)
			c.Assert(url.Revision, Equals, i) // Untouched.
			c.Assert(cerr, IsNil)
			c.Assert(string(data), Equals, string(t.data))
			c.Assert(err, IsNil)
		}
	}

	info, rc, err := s.store.OpenCharm(urlA.WithRevision(1))
	c.Assert(err, Equals, store.ErrNotFound)
	c.Assert(info, IsNil)
	c.Assert(rc, IsNil)
}

func (s *StoreSuite) TestLockUpdates(c *C) {
	urlA := charm.MustParseURL("cs:oneiric/wordpress-a")
	urlB := charm.MustParseURL("cs:oneiric/wordpress-b")
	urls := []*charm.URL{urlA, urlB}

	// Lock update of just B to force a partial conflict.
	lock1, err := s.store.LockUpdates(urls[1:])
	c.Assert(err, IsNil)

	// Partially conflicts with locked update above.
	lock2, err := s.store.LockUpdates(urls)
	c.Check(err, Equals, store.ErrUpdateConflict)
	c.Check(lock2, IsNil)

	lock1.Unlock()

	// Trying again should work since lock1 was released.
	lock3, err := s.store.LockUpdates(urls)
	c.Assert(err, IsNil)
	lock3.Unlock()
}

func (s *StoreSuite) TestLockUpdatesExpires(c *C) {
	urlA := charm.MustParseURL("cs:oneiric/wordpress-a")
	urlB := charm.MustParseURL("cs:oneiric/wordpress-b")
	urls := []*charm.URL{urlA, urlB}

	// Initiate an update of B only to force a partial conflict.
	lock1, err := s.store.LockUpdates(urls[1:])
	c.Assert(err, IsNil)

	// Hack time to force an expiration.
	locks := s.Session.DB("juju").C("locks")
	selector := bson.M{"_id": urlB.String()}
	update := bson.M{"time": bson.Now().Add(-store.UpdateTimeout - 10e9)}
	err = locks.Update(selector, update)
	c.Check(err, IsNil)

	// Works due to expiration of previous lock.
	lock2, err := s.store.LockUpdates(urls)
	c.Assert(err, IsNil)
	defer lock2.Unlock()

	// The expired lock was forcefully killed. Unlocking it must
	// not interfere with lock2 which is still alive.
	lock1.Unlock()

	// The above statement was a NOOP and lock2 is still in effect,
	// so attempting another lock must necessarily fail.
	lock3, err := s.store.LockUpdates(urls)
	c.Check(err, Equals, store.ErrUpdateConflict)
	c.Check(lock3, IsNil)
}

func (s *StoreSuite) TestConflictingUpdate(c *C) {
	// This test checks that if for whatever reason the locking
	// safety-net fails, adding two charms in parallel still
	// results in a sane outcome.
	url := charm.MustParseURL("cs:oneiric/wordpress")
	urls := []*charm.URL{url}

	pub1, err := s.store.CharmPublisher(urls, "some-digest")
	c.Assert(err, IsNil)
	c.Assert(pub1.Revision(), Equals, 0)

	pub2, err := s.store.CharmPublisher(urls, "some-digest")
	c.Assert(err, IsNil)
	c.Assert(pub2.Revision(), Equals, 0)

	// The first publishing attempt should work.
	err = pub2.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)

	// Attempting to finish the second attempt should break,
	// since it lost the race and the given revision is already
	// in place.
	err = pub1.Publish(&FakeCharmDir{})
	c.Assert(err, Equals, store.ErrUpdateConflict)
}

func (s *StoreSuite) TestRedundantUpdate(c *C) {
	urlA := charm.MustParseURL("cs:oneiric/wordpress-a")
	urlB := charm.MustParseURL("cs:oneiric/wordpress-b")
	urls := []*charm.URL{urlA, urlB}

	pub, err := s.store.CharmPublisher(urls, "digest-0")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 0)
	err = pub.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)

	// All charms are already on digest-0.
	pub, err = s.store.CharmPublisher(urls, "digest-0")
	c.Assert(err, ErrorMatches, "charm is up-to-date")
	c.Assert(err, Equals, store.ErrRedundantUpdate)
	c.Assert(pub, IsNil)

	// Now add a second revision just for wordpress-b.
	pub, err = s.store.CharmPublisher(urls[1:], "digest-1")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 1)
	err = pub.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)

	// Same digest bumps revision because one of them was old.
	pub, err = s.store.CharmPublisher(urls, "digest-1")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 2)
	err = pub.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)
}

const fakeRevZeroSha = "319095521ac8a62fa1e8423351973512ecca8928c9f62025e37de57c9ef07a53"

func (s *StoreSuite) TestCharmBundleData(c *C) {
	url := charm.MustParseURL("cs:oneiric/wordpress")
	urls := []*charm.URL{url}

	pub, err := s.store.CharmPublisher(urls, "key")
	c.Assert(err, IsNil)
	c.Assert(pub.Revision(), Equals, 0)

	err = pub.Publish(&FakeCharmDir{})
	c.Assert(err, IsNil)

	info, rc, err := s.store.OpenCharm(url)
	c.Assert(err, IsNil)
	c.Check(info.BundleSha256(), Equals, fakeRevZeroSha)
	c.Check(info.BundleSize(), Equals, int64(len("charm-revision-0")))
	err = rc.Close()
	c.Check(err, IsNil)
}

func (s *StoreSuite) TestLogCharmEventWithRevisionedURL(c *C) {
	url := charm.MustParseURL("cs:oneiric/wordpress-0")
	event := &store.CharmEvent{
		Kind:   store.EventPublishError,
		Digest: "some-digest",
		URLs:   []*charm.URL{url},
	}
	err := s.store.LogCharmEvent(event)
	c.Assert(err, ErrorMatches, "LogCharmEvent: got charm URL with revision: cs:oneiric/wordpress-0")

	// This may work in the future, but not now.
	event, err = s.store.CharmEvent(url, "some-digest")
	c.Assert(err, ErrorMatches, "CharmEvent: got charm URL with revision: cs:oneiric/wordpress-0")
	c.Assert(event, IsNil)
}

func (s *StoreSuite) TestLogCharmEvent(c *C) {
	url1 := charm.MustParseURL("cs:oneiric/wordpress")
	url2 := charm.MustParseURL("cs:oneiric/mysql")
	urls := []*charm.URL{url1, url2}

	event1 := &store.CharmEvent{
		Kind:     store.EventPublished,
		Revision: 42,
		Digest:   "revKey1",
		URLs:     urls,
		Warnings: []string{"A warning."},
		Time:     time.Unix(1, 0),
	}
	event2 := &store.CharmEvent{
		Kind:     store.EventPublished,
		Revision: 42,
		Digest:   "revKey2",
		URLs:     urls,
		Time:     time.Unix(1, 0),
	}
	event3 := &store.CharmEvent{
		Kind:   store.EventPublishError,
		Digest: "revKey2",
		Errors: []string{"An error."},
		URLs:   urls[:1],
	}

	for _, event := range []*store.CharmEvent{event1, event2, event3} {
		err := s.store.LogCharmEvent(event)
		c.Assert(err, IsNil)
	}

	events := s.Session.DB("juju").C("events")
	var s1, s2 map[string]interface{}

	err := events.Find(bson.M{"digest": "revKey1"}).One(&s1)
	c.Assert(err, IsNil)
	c.Assert(s1["kind"], Equals, int(store.EventPublished))
	c.Assert(s1["urls"], DeepEquals, []interface{}{"cs:oneiric/wordpress", "cs:oneiric/mysql"})
	c.Assert(s1["warnings"], DeepEquals, []interface{}{"A warning."})
	c.Assert(s1["errors"], IsNil)
	c.Assert(s1["time"], DeepEquals, time.Unix(1, 0))

	err = events.Find(bson.M{"digest": "revKey2", "kind": store.EventPublishError}).One(&s2)
	c.Assert(err, IsNil)
	c.Assert(s2["urls"], DeepEquals, []interface{}{"cs:oneiric/wordpress"})
	c.Assert(s2["warnings"], IsNil)
	c.Assert(s2["errors"], DeepEquals, []interface{}{"An error."})
	c.Assert(s2["time"].(time.Time).After(bson.Now().Add(-10e9)), Equals, true)

	// Mongo stores timestamps in milliseconds, so chop
	// off the extra bits for comparison.
	event3.Time = time.Unix(0, event3.Time.UnixNano()/1e6*1e6)

	event, err := s.store.CharmEvent(urls[0], "revKey2")
	c.Assert(err, IsNil)
	c.Assert(event, DeepEquals, event3)

	event, err = s.store.CharmEvent(urls[1], "revKey1")
	c.Assert(err, IsNil)
	c.Assert(event, DeepEquals, event1)

	event, err = s.store.CharmEvent(urls[1], "revKeyX")
	c.Assert(err, Equals, store.ErrNotFound)
	c.Assert(event, IsNil)
}

func (s *StoreSuite) TestSumCounters(c *C) {
	req := store.CounterRequest{Key: []string{"a"}}
	cs, err := s.store.Counters(&req)
	c.Assert(err, IsNil)
	c.Assert(cs, DeepEquals, []store.Counter{{Key: req.Key, Count: 0}})

	for i := 0; i < 10; i++ {
		err := s.store.IncCounter([]string{"a", "b", "c"})
		c.Assert(err, IsNil)
	}
	for i := 0; i < 7; i++ {
		s.store.IncCounter([]string{"a", "b"})
		c.Assert(err, IsNil)
	}
	for i := 0; i < 3; i++ {
		s.store.IncCounter([]string{"a", "z", "b"})
		c.Assert(err, IsNil)
	}

	tests := []struct {
		key    []string
		prefix bool
		result int64
	}{
		{[]string{"a", "b", "c"}, false, 10},
		{[]string{"a", "b"}, false, 7},
		{[]string{"a", "z", "b"}, false, 3},
		{[]string{"a", "b", "c"}, true, 0},
		{[]string{"a", "b", "c", "d"}, false, 0},
		{[]string{"a", "b"}, true, 10},
		{[]string{"a"}, true, 20},
		{[]string{"b"}, true, 0},
	}

	for _, t := range tests {
		c.Logf("Test: %#v\n", t)
		req = store.CounterRequest{Key: t.key, Prefix: t.prefix}
		cs, err := s.store.Counters(&req)
		c.Assert(err, IsNil)
		c.Assert(cs, DeepEquals, []store.Counter{{Key: t.key, Prefix: t.prefix, Count: t.result}})
	}

	// High-level interface works. Now check that the data is
	// stored correctly.
	counters := s.Session.DB("juju").C("stat.counters")
	docs1, err := counters.Count()
	c.Assert(err, IsNil)
	if docs1 != 3 && docs1 != 4 {
		fmt.Errorf("Expected 3 or 4 docs in counters collection, got %d", docs1)
	}

	// Hack times so that the next operation adds another document.
	err = counters.Update(nil, bson.D{{"$set", bson.D{{"t", 1}}}})
	c.Check(err, IsNil)

	err = s.store.IncCounter([]string{"a", "b", "c"})
	c.Assert(err, IsNil)

	docs2, err := counters.Count()
	c.Assert(err, IsNil)
	c.Assert(docs2, Equals, docs1+1)

	req = store.CounterRequest{Key: []string{"a", "b", "c"}}
	cs, err = s.store.Counters(&req)
	c.Assert(err, IsNil)
	c.Assert(cs, DeepEquals, []store.Counter{{Key: req.Key, Count: 11}})

	req = store.CounterRequest{Key: []string{"a"}, Prefix: true}
	cs, err = s.store.Counters(&req)
	c.Assert(err, IsNil)
	c.Assert(cs, DeepEquals, []store.Counter{{Key: req.Key, Prefix: true, Count: 21}})
}

func (s *StoreSuite) TestCountersReadOnlySum(c *C) {
	// Summing up an unknown key shouldn't add the key to the database.
	req := store.CounterRequest{Key: []string{"a", "b", "c"}}
	_, err := s.store.Counters(&req)
	c.Assert(err, IsNil)

	tokens := s.Session.DB("juju").C("stat.tokens")
	n, err := tokens.Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 0)
}

func (s *StoreSuite) TestCountersTokenCaching(c *C) {
	assertSum := func(i int, want int64) {
		req := store.CounterRequest{Key: []string{strconv.Itoa(i)}}
		cs, err := s.store.Counters(&req)
		c.Assert(err, IsNil)
		c.Assert(cs[0].Count, Equals, want)
	}
	assertSum(100000, 0)

	const genSize = 1024

	// All of these will be cached, as we have two generations
	// of genSize entries each.
	for i := 0; i < genSize*2; i++ {
		err := s.store.IncCounter([]string{strconv.Itoa(i)})
		c.Assert(err, IsNil)
	}

	// Now go behind the scenes and corrupt all the tokens.
	tokens := s.Session.DB("juju").C("stat.tokens")
	iter := tokens.Find(nil).Iter()
	var t struct {
		Id    int    "_id"
		Token string "t"
	}
	for iter.Next(&t) {
		err := tokens.UpdateId(t.Id, bson.M{"$set": bson.M{"t": "corrupted" + t.Token}})
		c.Assert(err, IsNil)
	}
	c.Assert(iter.Err(), IsNil)

	// We can consult the counters for the cached entries still.
	// First, check that the newest generation is good.
	for i := genSize; i < genSize*2; i++ {
		assertSum(i, 1)
	}

	// Now, we can still access a single entry of the older generation,
	// but this will cause the generations to flip and thus the rest
	// of the old generation will go away as the top half of the
	// entries is turned into the old generation.
	assertSum(0, 1)

	// Now we've lost access to the rest of the old generation.
	for i := 1; i < genSize; i++ {
		assertSum(i, 0)
	}

	// But we still have all of the top half available since it was
	// moved into the old generation.
	for i := genSize; i < genSize*2; i++ {
		assertSum(i, 1)
	}
}

func (s *StoreSuite) TestCounterTokenUniqueness(c *C) {
	var wg0, wg1 sync.WaitGroup
	wg0.Add(10)
	wg1.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			wg0.Done()
			wg0.Wait()
			defer wg1.Done()
			err := s.store.IncCounter([]string{"a"})
			c.Check(err, IsNil)
		}()
	}
	wg1.Wait()

	req := store.CounterRequest{Key: []string{"a"}}
	cs, err := s.store.Counters(&req)
	c.Assert(err, IsNil)
	c.Assert(cs[0].Count, Equals, int64(10))
}

func (s *StoreSuite) TestListCounters(c *C) {
	incs := [][]string{
		{"c", "b", "a"}, // Assign internal id c < id b < id a, to make sorting slightly trickier.
		{"a"},
		{"a", "c"},
		{"a", "b"},
		{"a", "b", "c"},
		{"a", "b", "c"},
		{"a", "b", "e"},
		{"a", "b", "d"},
		{"a", "f", "g"},
		{"a", "f", "h"},
		{"a", "i"},
		{"a", "i", "j"},
		{"k", "l"},
	}
	for _, key := range incs {
		err := s.store.IncCounter(key)
		c.Assert(err, IsNil)
	}

	tests := []struct {
		prefix []string
		result []store.Counter
	}{
		{
			[]string{"a"},
			[]store.Counter{
				{Key: []string{"a", "b"}, Prefix: true, Count: 4},
				{Key: []string{"a", "f"}, Prefix: true, Count: 2},
				{Key: []string{"a", "b"}, Prefix: false, Count: 1},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1},
				{Key: []string{"a", "i"}, Prefix: false, Count: 1},
				{Key: []string{"a", "i"}, Prefix: true, Count: 1},
			},
		}, {
			[]string{"a", "b"},
			[]store.Counter{
				{Key: []string{"a", "b", "c"}, Prefix: false, Count: 2},
				{Key: []string{"a", "b", "d"}, Prefix: false, Count: 1},
				{Key: []string{"a", "b", "e"}, Prefix: false, Count: 1},
			},
		}, {
			[]string{"z"},
			[]store.Counter(nil),
		},
	}

	// Use a different store to exercise cache filling.
	st, err := store.Open(s.Addr)
	c.Assert(err, IsNil)
	defer st.Close()

	for i := range tests {
		req := &store.CounterRequest{Key: tests[i].prefix, Prefix: true, List: true}
		result, err := st.Counters(req)
		c.Assert(err, IsNil)
		c.Assert(result, DeepEquals, tests[i].result)
	}
}

func (s *StoreSuite) TestListCountersBy(c *C) {
	incs := []struct {
		key []string
		day int
	}{
		{[]string{"a"}, 1},
		{[]string{"a"}, 1},
		{[]string{"b"}, 1},
		{[]string{"a", "b"}, 1},
		{[]string{"a", "c"}, 1},
		{[]string{"a"}, 3},
		{[]string{"a", "b"}, 3},
		{[]string{"b"}, 9},
		{[]string{"b"}, 9},
		{[]string{"a", "c", "d"}, 9},
		{[]string{"a", "c", "e"}, 9},
		{[]string{"a", "c", "f"}, 9},
	}

	day := func(i int) time.Time {
		return time.Date(2012, time.May, i, 0, 0, 0, 0, time.UTC)
	}

	counters := s.Session.DB("juju").C("stat.counters")
	for i, inc := range incs {
		err := s.store.IncCounter(inc.key)
		c.Assert(err, IsNil)

		// Hack time so counters are assigned to 2012-05-<day>
		filter := bson.M{"t": bson.M{"$gt": store.TimeToStamp(time.Date(2013, time.January, 1, 0, 0, 0, 0, time.UTC))}}
		stamp := store.TimeToStamp(day(inc.day))
		stamp += int32(i) * 60 // Make every entry unique.
		err = counters.Update(filter, bson.D{{"$set", bson.D{{"t", stamp}}}})
		c.Check(err, IsNil)
	}

	tests := []struct {
		request store.CounterRequest
		result  []store.Counter
	}{
		{
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: false,
				List:   false,
				By:     store.ByDay,
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: false, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: false, Count: 1, Time: day(3)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     store.ByDay,
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     store.ByDay,
				Start:  day(2),
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     store.ByDay,
				Stop:   day(4),
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 2, Time: day(1)},
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     store.ByDay,
				Start:  day(3),
				Stop:   day(8),
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 1, Time: day(3)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   true,
				By:     store.ByDay,
			},
			[]store.Counter{
				{Key: []string{"a", "b"}, Prefix: false, Count: 1, Time: day(1)},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1, Time: day(1)},
				{Key: []string{"a", "b"}, Prefix: false, Count: 1, Time: day(3)},
				{Key: []string{"a", "c"}, Prefix: true, Count: 3, Time: day(9)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   false,
				By:     store.ByWeek,
			},
			[]store.Counter{
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(6)},
				{Key: []string{"a"}, Prefix: true, Count: 3, Time: day(13)},
			},
		}, {
			store.CounterRequest{
				Key:    []string{"a"},
				Prefix: true,
				List:   true,
				By:     store.ByWeek,
			},
			[]store.Counter{
				{Key: []string{"a", "b"}, Prefix: false, Count: 2, Time: day(6)},
				{Key: []string{"a", "c"}, Prefix: false, Count: 1, Time: day(6)},
				{Key: []string{"a", "c"}, Prefix: true, Count: 3, Time: day(13)},
			},
		},
	}

	for _, test := range tests {
		result, err := s.store.Counters(&test.request)
		c.Assert(err, IsNil)
		c.Assert(result, DeepEquals, test.result)
	}
}

func (s *TrivialSuite) TestEventString(c *C) {
	c.Assert(store.EventPublished, Matches, "published")
	c.Assert(store.EventPublishError, Matches, "publish-error")
	for kind := store.CharmEventKind(1); kind < store.EventKindCount; kind++ {
		// This guarantees the switch in String is properly
		// updated with new event kinds.
		c.Assert(kind.String(), Matches, "[a-z-]+")
	}
}
