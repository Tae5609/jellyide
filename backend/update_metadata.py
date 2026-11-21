import argparse
import os
import sys

from mutagen.easyid3 import EasyID3
from mutagen.flac import FLAC
from mutagen.id3 import ID3, ID3NoHeaderError
from mutagen.mp4 import MP4
from mutagen.oggvorbis import OggVorbis


def update_metadata(file_path, title=None, album=None):
    if not os.path.exists(file_path):
        print(f"File not found: {file_path}")
        sys.exit(1)

    ext = os.path.splitext(file_path)[1].lower()

    try:
        if ext == ".flac":
            audio = FLAC(file_path)
            if title:
                audio["title"] = title
            if album:
                audio["album"] = album
            audio.save()
        elif ext == ".mp3":
            try:
                audio = EasyID3(file_path)
            except ID3NoHeaderError:
                id3 = ID3()
                id3.save(file_path)
                audio = EasyID3(file_path)
            if title:
                audio["title"] = title
            if album:
                audio["album"] = album
            audio.save()
        elif ext in [".m4a", ".mp4"]:
            audio = MP4(file_path)
            if title:
                audio["\xa9nam"] = title
            if album:
                audio["\xa9alb"] = album
            audio.save()
        elif ext == ".ogg":
            audio = OggVorbis(file_path)
            if title:
                audio["title"] = title
            if album:
                audio["album"] = album
            audio.save()
        else:
            print(f"Unsupported extension: {ext}")
            sys.exit(1)
        print(f"Successfully updated: {file_path}")
    except Exception as e:
        print(f"Error updating {file_path}: {str(e)}")
        sys.exit(1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("file_path")
    parser.add_argument("--title")
    parser.add_argument("--album")
    args = parser.parse_args()

    update_metadata(args.file_path, title=args.title, album=args.album)
