import hashlib
import os
import pathlib
import sys
import tempfile
import urllib.request


URL = "https://packages.fhir.org/hl7.fhir.r5.core/5.0.0"
SHA256 = "74b27cd1bfce9e80eaceac431edf230b0945a443564fbf5512f82e5fa50a80d4"


def digest(path):
    hasher = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def fetch(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        actual = digest(path)
        if actual != SHA256:
            raise ValueError(f"cached FHIR package checksum mismatch for {path}: {actual}")
        return

    descriptor, temporary_name = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    os.close(descriptor)
    temporary_path = pathlib.Path(temporary_name)
    try:
        with urllib.request.urlopen(URL, timeout=60) as response, temporary_path.open("wb") as destination:
            while chunk := response.read(1024 * 1024):
                destination.write(chunk)
        actual = digest(temporary_path)
        if actual != SHA256:
            raise ValueError(f"downloaded FHIR package checksum mismatch: {actual}")
        os.replace(temporary_path, path)
    finally:
        temporary_path.unlink(missing_ok=True)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: fetch_fhir_core.py OUTPUT_PATH")
    fetch(pathlib.Path(sys.argv[1]))
