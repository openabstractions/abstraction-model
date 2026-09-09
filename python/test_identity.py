import os
import unittest

import abstraction_model as m

FIXTURE = os.path.join(os.path.dirname(__file__), "..", "testdata", "identifiers.tsv")


def rows():
    with open(FIXTURE, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\r\n")
            if line and not line.startswith("#"):
                yield line.split("\t")


class Identity(unittest.TestCase):
    def test_fixture(self):
        n = 0
        for r in rows():
            got = m.parse(r[0])
            self.assertEqual((got.family, got.quant), (r[1], r[2]), r[0])
            n += 1
        self.assertGreater(n, 0)

    def test_fine_tune_never_joins_its_base(self):
        base = m.family("Gemma-4-26B-A4B-it-GGUF")
        for tuned in ("gemma-4-26b-a4b-it-ultra-uncensored-heretic",
                      "llmfan46/gemma-4-26B-A4B-it-abliterated-GGUF",
                      "someone/gemma-4-26b-a4b-it-distill"):
            self.assertNotEqual(m.family(tuned), base, tuned)

    def test_quantisation_does_not_change_the_family(self):
        want = m.family("qwen/qwen3.6-35b-a3b")
        for s in ("Qwen3.6-35B-A3B-GGUF",
                  "Qwen3.6-35B-A3B-MTP-GGUF",
                  "lmstudio-community/Qwen3.6-35B-A3B-GGUF/Qwen3.6-35B-A3B-Q4_K_M.gguf",
                  "unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf"):
            self.assertEqual(m.family(s), want, s)
        a = m.parse("lmstudio-community/Qwen3.6-35B-A3B-GGUF/Qwen3.6-35B-A3B-Q4_K_M.gguf")
        b = m.parse("unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
        self.assertNotEqual(a.quant, b.quant)

    def test_nothing_useful_survives_an_empty_name(self):
        for s in ("", "   ", "gguf", "hf://"):
            self.assertEqual(m.family(s), "", s)


if __name__ == "__main__":
    unittest.main()
