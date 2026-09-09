import sys

import abstraction_model as m

for line in sys.stdin:
    line = line.rstrip("\r\n")
    if not line or line.startswith("#"):
        continue
    n = m.parse(line.split("\t", 1)[0])
    sys.stdout.buffer.write(("%s\t%s\n" % (n.family, n.quant)).encode("utf-8"))
sys.stdout.buffer.flush()
