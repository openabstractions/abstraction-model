import json, os, tempfile, threading, time, unittest, urllib.error, urllib.request
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import resident

resident.MERGES_FILE = os.path.join(tempfile.gettempdir(), "no-such-model-merges")


class Fake:
    def __init__(self, catalogue=(), loaded=()):
        self.catalogue, self.loaded = list(catalogue), list(loaded)
        self.loads, self.unloads, self.hits = [], [], 0
        self.drop_chat = self.refuse_load = self.refuse_unload = False
        me = self

        class H(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *a):
                pass

            def reply(self, code, obj):
                b = json.dumps(obj).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(b)))
                self.end_headers()
                self.wfile.write(b)

            def do_GET(self):
                me.hits += 1
                if self.path == "/api/v1/models":
                    return self.reply(200, {"data": [{"id": m, "downloaded": True}
                                                     for m in me.catalogue]})
                if self.path == "/api/v1/health":
                    return self.reply(200, {"all_models_loaded": [
                        {"model_name": m, "loaded": True} for m in me.loaded]})
                if self.path == "/api/v0/models":
                    return self.reply(200, {"data": [
                        {"id": m, "type": "llm",
                         "state": "loaded" if m in me.loaded else "not-loaded"}
                        for m in me.catalogue]})
                if self.path == "/api/tags":
                    return self.reply(200, {"models": [{"name": m} for m in me.catalogue]})
                if self.path == "/api/ps":
                    return self.reply(200, {"models": [{"name": m} for m in me.loaded]})
                if self.path == "/api/v1/models":
                    return self.reply(200, {"models": [
                        {"key": m, "loaded_instances": [{"id": m + ":1"}] if m in me.loaded else []}
                        for m in me.catalogue]})
                self.reply(404, {})

            def unload(self, name):
                if me.refuse_unload:
                    return self.reply(500, {"error": "cannot unload"})
                me.unloads.append(name)
                me.loaded[:] = [m for m in me.loaded if m != name]
                self.reply(200, {"ok": True})

            def do_POST(self):
                me.hits += 1
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0))
                                  or b"{}")
                if self.path == "/api/v1/unload":
                    return self.unload(body.get("model_name"))
                if self.path == "/api/v1/models/unload":
                    return self.unload(body.get("instance_id", "").rsplit(":", 1)[0])
                if self.path == "/api/generate" and body.get("keep_alive") == 0:
                    return self.unload(body.get("model"))
                if self.path in ("/api/v1/load", "/api/generate"):
                    if me.refuse_load:
                        return self.reply(500, {"error": "cannot load"})
                    name = body.get("model_name") or body.get("model")
                    me.loads.append(name)
                    me.loaded.append(name)
                    return self.reply(200, {"ok": True})
                if me.drop_chat:
                    self.close_connection = True
                    return
                self.reply(200, {"choices": [{"message": {"content": "hello from " + me.tag}}]})

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.port = self.server.server_address[1]
        self.tag = "127.0.0.1:%d" % self.port
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    @property
    def base(self):
        return "http://127.0.0.1:%d" % self.port

    def stop(self):
        self.server.shutdown()
        self.server.server_close()


def broker(hosts):
    resident.HOSTS[:] = hosts
    resident.STATE.update(hosts={}, decisions=[], gpu_holders=[], gpu_readable=False, evicted=[])
    resident.clock = lambda: datetime.now(timezone.utc)
    resident.STORE = resident.job.MemoryStore(now=lambda: resident.clock())
    resident.RECALL_GRACE = 30.0
    resident.LOADING.clear()
    for h in hosts:
        h.down_until = 0.0


def get(url):
    with urllib.request.urlopen(url, timeout=10) as r:
        return json.loads(r.read())


def post(url, body):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.status, json.loads(r.read())


class Naming(unittest.TestCase):
    def test_three_spellings_are_one_family(self):
        names = ["Qwen3.6-35B-A3B-GGUF", "qwen/qwen3.6-35b-a3b", "qwen3.6-35b-a3b:latest"]
        self.assertEqual(len({resident.family(n) for n in names}), 1)

    def test_quantisation_and_variant_suffixes_do_not_split_a_family(self):
        self.assertEqual(resident.family("Llama-3.1-8B-Instruct-Q4_K_M.gguf"),
                         resident.family("llama-3.1-8b"))

    def test_a_parameter_count_is_not_mistaken_for_noise(self):
        self.assertNotEqual(resident.family("llama3:8b"), resident.family("llama3:70b"))


class Liveness(unittest.TestCase):
    def setUp(self):
        resident.DOWN_BACKOFF = 0.4
        self.fake = Fake(catalogue=["a-model"], loaded=["a-model"])
        self.host = resident.Ollama("ollama", self.fake.base)
        broker([self.host])

    def tearDown(self):
        self.fake.stop()

    def test_a_host_that_is_not_running_is_down_not_an_exception(self):
        self.fake.stop()
        s = self.host.read()
        self.assertFalse(s["up"])
        self.assertIn("reason", s)

    def test_a_down_host_is_not_polled_again_until_the_backoff_expires(self):
        self.fake.stop()
        self.host.read()
        before = self.fake.hits
        self.host.read()
        self.assertEqual(self.fake.hits, before)

    def test_a_host_that_appears_later_is_noticed_without_a_restart(self):
        self.fake.stop()
        self.assertFalse(self.host.read()["up"])
        back = Fake(catalogue=["a-model"], loaded=["a-model"])
        self.addCleanup(back.stop)
        self.host.base = back.base
        time.sleep(resident.DOWN_BACKOFF + 0.05)
        self.assertTrue(self.host.read()["up"])


class Routing(unittest.TestCase):
    def setUp(self):
        resident.DOWN_BACKOFF = 0.4
        self.lem = Fake(catalogue=["Qwen3.6-35B-A3B-GGUF"], loaded=["Qwen3.6-35B-A3B-GGUF"])
        self.oll = Fake(catalogue=["qwen3.6-35b-a3b:latest"])
        self.hosts = [resident.Lemonade("lemonade", self.lem.base),
                      resident.Ollama("ollama", self.oll.base)]
        broker(self.hosts)
        resident.survey()

    def tearDown(self):
        self.lem.stop()
        self.oll.stop()

    def test_a_resident_copy_is_reused_and_nothing_is_loaded(self):
        host, target = resident.place("qwen3.6-35b-a3b")
        self.assertEqual(host.name, "lemonade")
        self.assertEqual(self.oll.loads, [])

    def test_a_model_nobody_holds_is_loaded_on_a_host_that_has_it(self):
        self.lem.loaded.clear()
        resident.survey()
        host, target = resident.place("qwen3.6-35b-a3b")
        self.assertEqual(host.name, "lemonade")
        self.assertEqual(self.lem.loads, ["Qwen3.6-35B-A3B-GGUF"])

    def test_a_host_that_refuses_to_load_falls_through_to_the_next_host(self):
        self.lem.loaded.clear()
        self.lem.refuse_load = True
        resident.survey()
        host, target = resident.place("qwen3.6-35b-a3b")
        self.assertEqual(host.name, "ollama")
        self.assertEqual(self.oll.loads, ["qwen3.6-35b-a3b:latest"])

    def test_a_model_no_host_has_is_placed_nowhere(self):
        self.assertEqual(resident.place("no-such-model"), (None, None))

    def test_loading_one_family_does_not_block_routing_of_another(self):
        self.lem.loaded.clear()
        resident.survey()
        held = resident.family_lock("qwen3.6-35b-a3b")
        held.acquire()
        self.addCleanup(held.release)
        done = threading.Event()
        threading.Thread(target=lambda: (resident.place("qwen3.6-35b-a3b"), done.set()),
                         daemon=True).start()
        other = Fake(catalogue=["mistral-7b"], loaded=["mistral-7b"])
        self.addCleanup(other.stop)
        resident.HOSTS.append(resident.Ollama("second", other.base))
        resident.survey()
        host, _ = resident.place("mistral-7b")
        self.assertEqual(host.name, "second")
        self.assertFalse(done.is_set())


class OverHttp(unittest.TestCase):
    def setUp(self):
        resident.DOWN_BACKOFF = 0.4
        self.lem = Fake(catalogue=["Qwen3.6-35B-A3B-GGUF"], loaded=["Qwen3.6-35B-A3B-GGUF"])
        self.oll = Fake(catalogue=["qwen3.6-35b-a3b:latest"], loaded=["qwen3.6-35b-a3b:latest"])
        broker([resident.Lemonade("lemonade", self.lem.base),
                resident.Ollama("ollama", self.oll.base)])
        resident.survey()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), resident.Handler)
        self.url = "http://127.0.0.1:%d" % self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.lem.stop()
        self.oll.stop()

    def ask(self):
        return post(self.url + "/v1/chat/completions",
                    {"model": "qwen3.6-35b-a3b", "messages": [{"role": "user", "content": "hi"}]})

    def test_a_request_is_answered_and_names_who_answered_it(self):
        status, out = self.ask()
        self.assertEqual(status, 200)
        self.assertEqual(out["x_resident"]["host"], "lemonade")

    def test_a_host_dropping_mid_request_is_retried_on_another_host(self):
        self.lem.drop_chat = True
        status, out = self.ask()
        self.assertEqual(status, 200)
        self.assertEqual(out["x_resident"]["host"], "ollama")

    def test_every_host_dying_is_a_503_not_a_hang(self):
        self.lem.stop()
        self.oll.stop()
        with self.assertRaises(urllib.error.HTTPError) as e:
            self.ask()
        self.assertEqual(e.exception.code, 503)

    def test_the_broker_serves_its_window_with_no_host_running(self):
        self.lem.stop()
        self.oll.stop()
        resident.survey()
        with urllib.request.urlopen(self.url + "/", timeout=10) as r:
            self.assertEqual(r.status, 200)

    def test_the_catalogue_is_the_union_of_the_hosts(self):
        self.assertEqual([m["id"] for m in get(self.url + "/v1/models")["data"]],
                         ["qwen3.6-35b-a3b"])

    def test_a_malformed_request_does_not_kill_the_broker(self):
        with self.assertRaises(urllib.error.HTTPError):
            post(self.url + "/v1/chat/completions", {"model": {"not": "a string"}})
        self.assertEqual(self.ask()[0], 200)


class Findings(unittest.TestCase):
    ABSENCES = {"nothing matched", "denied", "skipped", "redacted", "never persisted",
                "not retained", "rate limited", "unknown"}

    def check(self, doc):
        self.assertEqual(doc["version"], 1)
        for f in doc["findings"]:
            for k in ("key", "title", "lede", "source", "rows", "blind"):
                self.assertIn(k, f)
            self.assertIsInstance(f["rows"], list)
            if not f["rows"]:
                self.assertIn(f["absence"], self.ABSENCES, f["key"])
                self.assertTrue(f["because"].strip(), f["key"])
            for b in f["blind"]:
                self.assertIn(b["absence"], self.ABSENCES)

    def test_no_panel_is_empty_and_silent_when_every_host_is_down(self):
        broker([resident.Ollama("ollama", "http://127.0.0.1:1")])
        resident.survey()
        doc = resident.findings()
        self.check(doc)
        self.assertEqual([f["absence"] for f in doc["findings"] if not f["rows"]][0],
                         "nothing matched")

    def test_no_panel_is_empty_and_silent_when_a_host_is_up(self):
        fake = Fake(catalogue=["a-model"], loaded=["a-model"])
        self.addCleanup(fake.stop)
        broker([resident.Ollama("ollama", fake.base)])
        resident.survey()
        self.check(resident.findings())

    def test_an_unreachable_host_is_named_in_the_blind_list(self):
        broker([resident.Ollama("ollama", "http://127.0.0.1:1")])
        resident.survey()
        blind = [b["what"] for f in resident.findings()["findings"] for b in f["blind"]]
        self.assertIn("ollama", blind)

    def test_unreadable_gpu_counters_are_denied_not_empty(self):
        fake = Fake(catalogue=["a-model"], loaded=["a-model"])
        self.addCleanup(fake.stop)
        broker([resident.Ollama("ollama", fake.base)])
        resident.survey()
        gpu = [f for f in resident.findings()["findings"] if f["key"] == "gpu"][0]
        self.assertEqual(gpu["absence"], "denied")


class Doubled(unittest.TestCase):
    def setUp(self):
        self.a = Fake(catalogue=["Qwen3.6-35B-A3B-GGUF"], loaded=["Qwen3.6-35B-A3B-GGUF"])
        self.b = Fake(catalogue=["qwen/qwen3.6-35b-a3b"], loaded=["qwen/qwen3.6-35b-a3b"])
        self.addCleanup(self.a.stop)
        self.addCleanup(self.b.stop)
        broker([resident.Lemonade("lemonade", self.a.base),
                resident.LMStudio("lmstudio", self.b.base)])

    def rows(self):
        resident.survey()
        return [f for f in resident.findings()["findings"] if f["key"] == "doubled"][0]

    def test_one_model_held_by_two_hosts_is_named(self):
        p = self.rows()
        self.assertEqual([r["family"] for r in p["rows"]], ["qwen3.6-35b-a3b"])
        self.assertEqual(p["rows"][0]["copies"], 2)

    def test_one_copy_is_an_explained_absence_not_an_empty_panel(self):
        self.b.loaded.clear()
        p = self.rows()
        self.assertEqual(p["rows"], [])
        self.assertTrue(p["because"].strip())


class Recall(unittest.TestCase):
    """The doubled case closing. Two hosts hold one family; the broker records
    each copy as a job it holds on the host's behalf, recalls the one routing
    would not use, and the keeper honours the recall by unloading. Where the
    host will not unload, the lease lapses and the copy is starved instead."""

    A, B = "Qwen3.6-35B-A3B-GGUF", "qwen/qwen3.6-35b-a3b"
    FAMILY = "qwen3.6-35b-a3b"

    def setUp(self):
        self.a = Fake(catalogue=[self.A], loaded=[self.A])
        self.b = Fake(catalogue=[self.B], loaded=[self.B])
        self.addCleanup(self.a.stop)
        self.addCleanup(self.b.stop)
        broker([resident.Lemonade("lemonade", self.a.base),
                resident.LMStudio("lmstudio", self.b.base)])

    def tick(self):
        resident.survey()
        resident.reconcile()

    def doubled(self):
        return [f for f in resident.findings()["findings"] if f["key"] == "doubled"][0]

    def job_for(self, host, model):
        return resident.latest_jobs(resident.store())[(host, model)]

    def test_every_resident_copy_is_a_job_a_third_party_can_read(self):
        self.tick()
        held = sorted((r.spec["host"], r.spec["family"], r.state, r.lease.owner)
                      for r in resident.store().list() if r.kind == "resident")
        self.assertEqual(held, [("lemonade", self.FAMILY, "running", "resident@lemonade"),
                                ("lmstudio", self.FAMILY, "running", "resident@lmstudio")])

    def test_the_duplicate_is_recalled_and_actually_released(self):
        self.tick()
        self.assertEqual(self.job_for("lmstudio", self.B).lease.recall.reason,
                         "doubled: lemonade holds " + self.FAMILY)
        self.assertEqual(self.doubled()["rows"][0]["recall"], "lmstudio: asked, 30s to yield")
        self.tick()
        self.assertEqual(self.b.unloads, [self.B])
        self.assertEqual(self.a.unloads, [])
        self.tick()
        self.assertEqual(self.doubled()["rows"], [])
        done = self.job_for("lmstudio", self.B)
        self.assertEqual(done.state, "complete")
        self.assertTrue(done.lease.recalled())
        self.assertEqual(self.job_for("lemonade", self.A).state, "running")

    def test_a_copy_that_will_not_unload_is_evicted_and_starved(self):
        self.b.refuse_unload = True
        t = [datetime(2026, 9, 6, 12, 0, tzinfo=timezone.utc)]
        resident.clock = lambda: t[0]
        self.tick()
        self.tick()
        self.assertEqual(self.b.unloads, [])
        self.assertEqual(resident.STATE["decisions"][0]["kind"], "recall-refused")
        self.assertEqual(self.doubled()["rows"][0]["recall"], "lmstudio: asked, 30s to yield")
        t[0] += timedelta(seconds=31)
        self.tick()
        self.assertEqual(resident.STATE["evicted"], ["lmstudio/" + self.B])
        self.assertEqual(self.doubled()["rows"][0]["recall"], "lmstudio: evicted, not routed to")
        self.a.loaded.clear()
        self.tick()
        host, target = resident.place(self.FAMILY)
        self.assertEqual((host.name, self.a.loads), ("lemonade", [self.A]))
        self.b.loaded.clear()
        self.tick()
        self.assertEqual(self.job_for("lmstudio", self.B).state, "complete")
        self.assertEqual(resident.STATE["evicted"], [])

    def test_a_third_party_can_evict_through_the_store(self):
        self.b.loaded.clear()
        self.tick()
        r = self.job_for("lemonade", self.A)
        resident.store().recall(r.id, r.lease.epoch, "make room", "a person", 30)
        self.tick()
        self.assertEqual(self.a.unloads, [self.A])
        self.assertEqual(self.job_for("lemonade", self.A).state, "complete")


class GpuCounters(unittest.TestCase):
    def feed(self, gpu, procs):
        real = resident.read_counters
        resident.read_counters = lambda paths: gpu if paths is resident.GPU_COUNTERS else procs
        self.addCleanup(lambda: setattr(resident, "read_counters", real))

    def test_two_processes_sharing_an_image_name_are_summed_not_confused(self):
        self.feed([("pid_100_luid_0x0_0x1_phys_0", 3 * 2**30),
                   ("pid_200_luid_0x0_0x1_phys_0", 2 * 2**30)],
                  [("msedge", 100), ("msedge", 200), ("dwm", 300)])
        self.assertEqual(resident.gpu_holders(), [{"process": "msedge", "gib": 5.0}])

    def test_a_process_the_counters_cannot_name_is_shown_by_pid(self):
        self.feed([("pid_777_luid_0x0_0x1_phys_0", 4 * 2**30)], [])
        self.assertEqual(resident.gpu_holders(), [{"process": "pid777", "gib": 4.0}])

    def test_counters_that_cannot_be_read_are_none_not_an_empty_list(self):
        self.feed(None, None)
        self.assertIsNone(resident.gpu_holders())


class Merges(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.path = os.path.join(self.dir, "model-merges")
        self.saved, resident.MERGES_FILE = resident.MERGES_FILE, self.path
        self.addCleanup(lambda: setattr(resident, "MERGES_FILE", self.saved))
        self.addCleanup(lambda: setattr(resident, "MERGES", {}))
        self.lem = Fake(catalogue=["Qwen3.6-35B-A3B-GGUF"], loaded=["Qwen3.6-35B-A3B-GGUF"])
        self.oll = Fake(catalogue=["some-vendors-own-name-for-it"])
        self.addCleanup(self.lem.stop)
        self.addCleanup(self.oll.stop)
        broker([resident.Lemonade("lemonade", self.lem.base),
                resident.Ollama("ollama", self.oll.base)])

    def write(self, text):
        with open(self.path, "w", encoding="utf-8") as f:
            f.write(text)
        resident.survey()

    def test_a_name_the_parser_splits_loads_a_second_copy(self):
        self.write("")
        host, _ = resident.place(resident.family("some-vendors-own-name-for-it"))
        self.assertEqual(host.name, "ollama")
        self.assertEqual(self.oll.loads, ["some-vendors-own-name-for-it"])

    def test_the_same_name_merged_is_routed_to_the_resident_copy_instead(self):
        self.write("%s qwen3.6-35b-a3b\n" % resident.parsed_family("some-vendors-own-name-for-it"))
        host, _ = resident.place(resident.family("some-vendors-own-name-for-it"))
        self.assertEqual(host.name, "lemonade")
        self.assertEqual(self.oll.loads, [])

    def test_a_merge_takes_effect_without_a_restart(self):
        self.write("")
        self.assertNotEqual(resident.family("a-name"), "qwen3.6-35b-a3b")
        self.write("a-name qwen3.6-35b-a3b\n")
        self.assertEqual(resident.family("a-name"), "qwen3.6-35b-a3b")

    def test_a_merge_file_naming_a_cycle_does_not_hang(self):
        self.write("one two\ntwo one\n")
        self.assertIn(resident.family("one"), ("one", "two"))

    def test_a_comment_and_a_malformed_line_are_ignored(self):
        self.write("# a note\nrubbish\none two\n")
        self.assertEqual(resident.family("one"), "two")


class SingleInstance(unittest.TestCase):
    def test_a_second_broker_refuses_the_port_rather_than_stealing_it(self):
        first = resident.OnlyBroker(("127.0.0.1", 0), resident.Handler)
        self.addCleanup(first.server_close)
        with self.assertRaises(OSError):
            resident.OnlyBroker(("127.0.0.1", first.server_address[1]), resident.Handler)


class Poller(unittest.TestCase):
    def test_the_poller_outlives_a_survey_that_raises(self):
        broker([])
        stop = threading.Event()
        boom = [3]

        def explode():
            if boom[0] > 0:
                boom[0] -= 1
                raise RuntimeError("counters went away")
            return {}

        real, resident.survey = resident.survey, explode
        self.addCleanup(lambda: setattr(resident, "survey", real))
        t = threading.Thread(target=resident.poller, args=(stop,), daemon=True)
        t.start()
        time.sleep(0.3)
        stop.set()
        self.assertTrue(t.is_alive())
        self.assertGreater(len(resident.STATE["decisions"]), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
