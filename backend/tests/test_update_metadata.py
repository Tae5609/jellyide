"""Round-trip tests for backend/update_metadata.py.

One-second files are generated with ffmpeg (``-f lavfi -i anullsrc``); the
whole module is skipped when ffmpeg is missing, and individual formats are
skipped when the local ffmpeg build lacks their encoder.
"""

import json
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "update_metadata.py"
FFMPEG = shutil.which("ffmpeg")

pytestmark = pytest.mark.skipif(FFMPEG is None, reason="ffmpeg is required to generate test files")

# format -> (file extension, ffmpeg audio encoder)
GENERATORS = {
    "flac": ("flac", "flac"),
    "mp3": ("mp3", "libmp3lame"),
    "m4a": ("m4a", "aac"),
    "ogg": ("ogg", "libvorbis"),
}

FIELDS = {
    "title": "Track Title",
    "album": "Album Name",
    "artist": "Artist Name",
    "album_artist": "Album Artist Name",
    "track_number": 3,
    "disc_number": 2,
    "year": 2001,
    "genre": "Rock",
    "composer": "Composer Name",
    "comment": "A comment",
}


def run_script(*args, expect=0):
    """Run update_metadata.py with the given args and assert the exit code."""
    result = subprocess.run(
        [sys.executable, str(SCRIPT), *args],
        capture_output=True,
        text=True,
    )
    assert result.returncode == expect, (
        f"exit {result.returncode}, want {expect}; "
        f"stdout={result.stdout!r} stderr={result.stderr!r}"
    )
    return result


def make_file(tmp_path: Path, fmt: str) -> Path:
    ext, encoder = GENERATORS[fmt]
    out = tmp_path / f"track.{ext}"
    cmd = [
        FFMPEG,
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-f",
        "lavfi",
        "-i",
        "anullsrc=r=44100:cl=stereo",
        "-t",
        "1",
        "-c:a",
        encoder,
        str(out),
    ]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        pytest.skip(f"ffmpeg cannot encode {fmt}: {result.stderr.strip()[:200]}")
    return out


def make_cover(tmp_path: Path, fmt: str = "png") -> Path:
    out = tmp_path / f"cover.{fmt}"
    codec = "png" if fmt == "png" else "mjpeg"
    cmd = [
        FFMPEG,
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-f",
        "lavfi",
        "-i",
        "color=c=red:s=16x16",
        "-frames:v",
        "1",
        "-c:v",
        codec,
        str(out),
    ]
    result = subprocess.run(cmd, capture_output=True, text=True)
    assert result.returncode == 0, f"cover generation failed: {result.stderr.strip()[:200]}"
    return out


def read_tags(path: Path) -> dict:
    result = run_script("--read", "--", str(path))
    return json.loads(result.stdout)


@pytest.mark.parametrize("fmt", sorted(GENERATORS))
def test_read_untagged_file(tmp_path, fmt):
    path = make_file(tmp_path, fmt)
    tags = read_tags(path)
    assert tags == {field: None for field in FIELDS}, tags


@pytest.mark.parametrize("fmt", sorted(GENERATORS))
def test_roundtrip_fields(tmp_path, fmt):
    path = make_file(tmp_path, fmt)
    run_script("--fields-json", json.dumps(FIELDS), "--", str(path))
    tags = read_tags(path)
    for field, value in FIELDS.items():
        assert tags[field] == value, f"{fmt}: {field} = {tags[field]!r}, want {value!r}"


@pytest.mark.parametrize("fmt", sorted(GENERATORS))
def test_null_deletes_tag(tmp_path, fmt):
    path = make_file(tmp_path, fmt)
    run_script("--fields-json", json.dumps(FIELDS), "--", str(path))
    run_script(
        "--fields-json", json.dumps({"comment": None, "genre": None, "year": None}), "--", str(path)
    )
    tags = read_tags(path)
    assert tags["comment"] is None
    assert tags["genre"] is None
    assert tags["year"] is None
    # Untouched fields survive the clearing write.
    assert tags["title"] == FIELDS["title"]
    assert tags["track_number"] == FIELDS["track_number"]


def test_old_flags_still_work(tmp_path):
    path = make_file(tmp_path, "mp3")
    run_script("--title", "Old Style Title", "--album", "Old Style Album", "--", str(path))
    tags = read_tags(path)
    assert tags["title"] == "Old Style Title"
    assert tags["album"] == "Old Style Album"


def test_old_flags_combine_with_fields_json(tmp_path):
    path = make_file(tmp_path, "flac")
    run_script(
        "--title", "Flag Title", "--fields-json", json.dumps({"genre": "Jazz"}), "--", str(path)
    )
    tags = read_tags(path)
    assert tags["title"] == "Flag Title"
    assert tags["genre"] == "Jazz"


@pytest.mark.parametrize("fmt", sorted(GENERATORS))
@pytest.mark.parametrize("cover_fmt", ["png", "jpg"])
def test_roundtrip_cover(tmp_path, fmt, cover_fmt):
    path = make_file(tmp_path, fmt)
    cover = make_cover(tmp_path, cover_fmt)
    run_script("--cover", str(cover), "--", str(path))
    out = tmp_path / "extracted.bin"
    run_script("--extract-cover", str(out), "--", str(path))
    assert out.read_bytes() == cover.read_bytes(), f"{fmt}/{cover_fmt}: extracted cover differs"


@pytest.mark.parametrize("fmt", sorted(GENERATORS))
def test_extract_missing_cover_exits_2(tmp_path, fmt):
    path = make_file(tmp_path, fmt)
    out = tmp_path / "out.bin"
    run_script("--extract-cover", str(out), "--", str(path), expect=2)
    assert not out.exists()


def test_fields_json_invalid_json_exits_1(tmp_path):
    path = make_file(tmp_path, "flac")
    run_script("--fields-json", "{not json", "--", str(path), expect=1)


def test_fields_json_unknown_field_exits_1(tmp_path):
    path = make_file(tmp_path, "flac")
    run_script("--fields-json", json.dumps({"bogus_field": "x"}), "--", str(path), expect=1)


def test_missing_file_exits_1(tmp_path):
    run_script("--read", "--", str(tmp_path / "missing.flac"), expect=1)


def test_unsupported_extension_exits_1(tmp_path):
    path = tmp_path / "file.wav"
    path.write_bytes(b"not audio")
    run_script("--read", "--", str(path), expect=1)
