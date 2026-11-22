"""Read, update and inspect tags of audio files.

Supports FLAC, Ogg Vorbis, MP3 (ID3 via EasyID3) and M4A/MP4. Used by the
JellyIDE backend; the Go side always passes arguments as a list with a `--`
before the file path so the path can never be parsed as an option.

Exit codes: 0 success, 1 error, 2 no embedded cover (--extract-cover).
"""

import argparse
import base64
import json
import os
import re
import sys

from mutagen.easyid3 import EasyID3
from mutagen.flac import FLAC, Picture
from mutagen.id3 import APIC, COMM, ID3, ID3NoHeaderError
from mutagen.mp4 import MP4, MP4Cover
from mutagen.oggvorbis import OggVorbis

EXIT_ERROR = 1
EXIT_NO_COVER = 2

TEXT_FIELDS = ("title", "album", "artist", "album_artist", "genre", "composer", "comment")
INT_FIELDS = ("track_number", "disc_number", "year")
ALL_FIELDS = TEXT_FIELDS + INT_FIELDS

# Canonical field name -> native key candidates for comment-based tags
# (Vorbis comments and ID3 text frames). The first candidate is written on
# set; all candidates are removed on delete.
COMMENT_KEYS = {
    "title": ("title",),
    "album": ("album",),
    "artist": ("artist",),
    "album_artist": ("albumartist", "album_artist"),
    "track_number": ("tracknumber", "track"),
    "disc_number": ("discnumber", "disc"),
    "year": ("date", "year"),
    "genre": ("genre",),
    "composer": ("composer",),
    "comment": ("comment",),
}

# Canonical field name -> MP4 atom.
M4A_KEYS = {
    "title": "\xa9nam",
    "album": "\xa9alb",
    "artist": "\xa9ART",
    "album_artist": "aART",
    "track_number": "trkn",
    "disc_number": "disk",
    "year": "\xa9day",
    "genre": "\xa9gen",
    "composer": "\xa9wrt",
    "comment": "\xa9cmt",
}


def fail(message):
    print(message, file=sys.stderr)
    sys.exit(EXIT_ERROR)


def _ext(file_path):
    return os.path.splitext(file_path)[1].lower()


def _empty_tags():
    return {field: None for field in ALL_FIELDS}


def _to_int(raw):
    """Parse 3, '3', '3/12', '2001-05-15' style values to an int (or None)."""
    raw = str(raw).strip()
    if "/" in raw:
        raw = raw.split("/", 1)[0].strip()
    match = re.search(r"\d+", raw)
    if match is None:
        return None
    return int(match.group(0))


def _parse_field(field, raw):
    if raw is None:
        return None
    if field in INT_FIELDS:
        return _to_int(raw)
    return str(raw)


def _empty_tags_for_no_block():
    """All tags read as null for a file without any tag block."""
    return _empty_tags()


def _tag_values(audio, key):
    """Read one tag key as a list of strings (empty when absent)."""
    try:
        values = audio[key]
    except KeyError:
        return []
    return list(values or [])


def _read_comments(audio, keymap):
    tags = _empty_tags()
    for field, keys in keymap.items():
        for key in keys:
            values = _tag_values(audio, key)
            if values:
                tags[field] = _parse_field(field, values[0])
                break
    return tags


def _m4a_get(audio, atom):
    try:
        values = audio[atom]
    except KeyError:
        return None
    if not values:
        return None
    return values[0]


def _read_m4a(audio):
    tags = _empty_tags()
    for field, atom in M4A_KEYS.items():
        value = _m4a_get(audio, atom)
        if value is None:
            continue
        if field in ("track_number", "disc_number"):
            tags[field] = int(value[0])
        elif field == "year":
            tags[field] = _to_int(value)
        else:
            tags[field] = str(value)
    return tags


def read_tags(file_path):
    ext = _ext(file_path)
    if ext == ".flac":
        return _read_comments(FLAC(file_path), COMMENT_KEYS)
    if ext == ".ogg":
        return _read_comments(OggVorbis(file_path), COMMENT_KEYS)
    if ext == ".mp3":
        _ensure_easyid3_keys()
        _ensure_easyid3_comment()
        try:
            audio = EasyID3(file_path)
        except ID3NoHeaderError:
            return _empty_tags_for_no_block()
        return _read_comments(audio, COMMENT_KEYS)
    if ext in (".m4a", ".mp4"):
        return _read_m4a(MP4(file_path))
    fail(f"Unsupported extension: {ext}")


def _easyid3_registered_keys():
    registered = getattr(EasyID3, "Get", None) or getattr(EasyID3, "_key2id", None)
    if registered is None:
        return set()
    return set(registered)


def _ensure_easyid3_keys():
    """Register the EasyID3 keys this script needs on older mutagen releases."""
    missing = [
        key
        for key in ("albumartist", "composer", "tracknumber", "discnumber", "date", "genre")
        if key not in _easyid3_registered_keys()
    ]
    register = getattr(EasyID3, "register_key", None)
    if not missing or register is None:
        return
    frames = {
        "albumartist": "TPE2",
        "composer": "TCOM",
        "tracknumber": "TRCK",
        "discnumber": "TPOS",
        "date": "TDRC",
        "genre": "TCON",
    }
    for key in missing:
        try:
            register(key, frames[key])
        except Exception:  # noqa: BLE001 - a broken registration must not kill the run
            pass


def _ensure_easyid3_comment():
    """Map the canonical "comment" key onto (unparameterised) COMM frames."""
    if "comment" in _easyid3_registered_keys():
        return

    # mutagen >= 1.48: functional key registration, the callbacks receive the
    # raw ID3 object.
    register_key = getattr(EasyID3, "RegisterKey", None)
    if register_key is not None:

        def comm_frames(id3):
            return [
                frame for frame in id3.getall("COMM") if frame.desc == "" and frame.lang == "eng"
            ]

        def getter(id3, key):
            texts = []
            for frame in comm_frames(id3):
                if frame.text:
                    texts.append(frame.text[0])
            return texts

        def setter(id3, key, value):
            for frame in comm_frames(id3):
                del id3[frame.HashKey]
            if value:
                id3.add(COMM(encoding=3, lang="eng", desc="", text="".join(value)))

        def deleter(id3, key):
            for frame in comm_frames(id3):
                del id3[frame.HashKey]

        register_key("comment", getter=getter, setter=setter, deleter=deleter)
        return

    # Classic mutagen API: register the key against the COMM frame type.
    register = getattr(EasyID3, "register_key", None)
    if register is not None:
        try:
            register("comment", "COMM")
        except Exception:  # noqa: BLE001 - fall through, comment stays unsupported
            pass


def _open_easyid3(file_path):
    try:
        return EasyID3(file_path)
    except ID3NoHeaderError:
        ID3().save(file_path)
        return EasyID3(file_path)


def _write_comments(audio, keymap, fields):
    for field, value in fields.items():
        keys = keymap.get(field)
        if keys is None:
            continue
        for key in keys:
            try:
                del audio[key]
            except (KeyError, AttributeError):
                pass
        if value is not None:
            text = str(value) if field in INT_FIELDS else value
            audio[keys[0]] = text
    audio.save()


def _write_m4a(audio, fields):
    for field, value in fields.items():
        atom = M4A_KEYS.get(field)
        if atom is None:
            continue
        try:
            del audio[atom]
        except (KeyError, AttributeError):
            pass
        if value is not None:
            if field in ("track_number", "disc_number"):
                audio[atom] = [(int(value), 0)]
            elif field == "year":
                audio[atom] = [str(value)]
            else:
                audio[atom] = [value]
    audio.save()


def write_fields(file_path, fields):
    ext = _ext(file_path)
    if ext == ".flac":
        _write_comments(FLAC(file_path), COMMENT_KEYS, fields)
    elif ext == ".ogg":
        _write_comments(OggVorbis(file_path), COMMENT_KEYS, fields)
    elif ext == ".mp3":
        _ensure_easyid3_keys()
        _ensure_easyid3_comment()
        _write_comments(_open_easyid3(file_path), COMMENT_KEYS, fields)
    elif ext in (".m4a", ".mp4"):
        _write_m4a(MP4(file_path), fields)
    else:
        fail(f"Unsupported extension: {ext}")


def _cover_mime(cover_path):
    ext = _ext(cover_path)
    if ext in (".jpg", ".jpeg"):
        return "image/jpeg"
    if ext == ".png":
        return "image/png"
    fail(f"Unsupported cover type: {ext} (only JPEG and PNG are supported)")


def embed_cover(file_path, cover_path):
    if not os.path.exists(cover_path):
        fail(f"Cover not found: {cover_path}")
    with open(cover_path, "rb") as fh:
        data = fh.read()
    mime = _cover_mime(cover_path)
    ext = _ext(file_path)
    if ext == ".flac":
        audio = FLAC(file_path)
        pic = Picture()
        pic.data = data
        pic.mime = mime
        pic.type = 3  # cover (front)
        if hasattr(audio, "add_picture"):  # mutagen >= 1.48
            audio.clear_pictures()
            audio.add_picture(pic)
        else:
            audio.pictures = [pic]
        audio.save()
    elif ext == ".ogg":
        audio = OggVorbis(file_path)
        block = b"application/octet-stream\x00cover\x00" + mime.encode("ascii") + b"\x00" + data
        audio["metadata_block_picture"] = [base64.b64encode(block).decode("ascii")]
        audio.save()
    elif ext == ".mp3":
        try:
            id3 = ID3(file_path)
        except ID3NoHeaderError:
            id3 = ID3()
            id3.save(file_path)
        id3["APIC"] = APIC(encoding=3, mime=mime, type=3, desc="", data=data)
        id3.save()
    elif ext in (".m4a", ".mp4"):
        audio = MP4(file_path)
        image_format = MP4Cover.FORMAT_PNG if mime == "image/png" else MP4Cover.FORMAT_JPEG
        audio["covr"] = [MP4Cover(data, imageformat=image_format)]
        audio.save()
    else:
        fail(f"Unsupported extension: {ext}")


def _parse_picture_block(block):
    parts = block.split(b"\x00", 3)
    if len(parts) != 4 or not parts[3]:
        return None
    return parts[3]


def extract_cover(file_path):
    """Return the embedded cover bytes, or None when the file has none."""
    ext = _ext(file_path)
    if ext == ".flac":
        for pic in FLAC(file_path).pictures:
            if pic.data:
                return pic.data
        return None
    if ext == ".ogg":
        audio = OggVorbis(file_path)
        for encoded in _tag_values(audio, "metadata_block_picture"):
            data = _parse_picture_block(base64.b64decode(str(encoded)))
            if data:
                return data
        return None
    if ext == ".mp3":
        try:
            id3 = ID3(file_path)
        except ID3NoHeaderError:
            return None
        for frame in id3.getall("APIC"):
            if frame.data:
                return frame.data
        return None
    if ext in (".m4a", ".mp4"):
        audio = MP4(file_path)
        try:
            covers = audio["covr"]
        except KeyError:
            return None
        for cover in covers:
            data = getattr(cover, "data", None)
            if data is None and isinstance(cover, (bytes, bytearray)):
                # mutagen >= 1.48 stores the cover bytes directly.
                data = bytes(cover)
            if data:
                return data
        return None
    fail(f"Unsupported extension: {ext}")


def main():
    parser = argparse.ArgumentParser(description="Read, update and inspect audio file tags.")
    parser.add_argument("file_path", nargs="?")
    parser.add_argument("--title")
    parser.add_argument("--album")
    parser.add_argument("--fields-json", dest="fields_json")
    parser.add_argument("--cover", dest="cover_path")
    parser.add_argument("--extract-cover", dest="extract_cover")
    parser.add_argument("--read", action="store_true")
    args = parser.parse_args()

    if not args.file_path:
        fail("missing file path")
    if not os.path.exists(args.file_path):
        fail(f"File not found: {args.file_path}")

    if args.read:
        print(json.dumps(read_tags(args.file_path)))
        return

    if args.extract_cover:
        data = extract_cover(args.file_path)
        if data is None:
            print(f"No embedded cover in {args.file_path}", file=sys.stderr)
            sys.exit(EXIT_NO_COVER)
        with open(args.extract_cover, "wb") as fh:
            fh.write(data)
        return

    if args.cover_path:
        embed_cover(args.file_path, args.cover_path)
        print(f"Embedded cover: {args.file_path}")
        return

    fields = {}
    if args.title is not None:
        fields["title"] = args.title
    if args.album is not None:
        fields["album"] = args.album
    if args.fields_json is not None:
        try:
            parsed = json.loads(args.fields_json)
        except json.JSONDecodeError as exc:
            fail(f"Invalid --fields-json: {exc}")
        if not isinstance(parsed, dict):
            fail("--fields-json must be a JSON object")
        unknown = sorted(set(parsed) - set(ALL_FIELDS))
        if unknown:
            fail(f"Unknown fields: {', '.join(unknown)}")
        fields.update(parsed)

    if not fields:
        fail("nothing to do: pass --title, --album, --fields-json, --cover or --read")
    write_fields(args.file_path, fields)
    print(f"Successfully updated: {args.file_path}")


if __name__ == "__main__":
    main()
