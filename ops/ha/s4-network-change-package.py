import sys
import os

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..")))

from ops.ha.s4_network_change_package import main


if __name__ == "__main__":
    raise SystemExit(main())
