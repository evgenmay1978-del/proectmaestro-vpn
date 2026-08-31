import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from ops.ha.s4_network_change_package import main


if __name__ == "__main__":
    raise SystemExit(main())
