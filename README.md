# Photo Organizer

Simple app to organize images, copying files from source to the destination with a folder and file name convetion.

Convetion:
```
/<year>/<month>/<year>-<month>-<day>-<Hour><minute><seconds>-<EXIF camera model>-<counter>.(jpg|arw|mp4)

e.g. /2013/10/2013-10-05-030421-nexf3-00.jpg
```

GoPro `.mp4` videos are imported too: the date comes from the MP4 movie header and the
camera model from the GoPro GPMF metadata. Other MP4s are rejected, and GoPro `.lrv`/`.thm`
preview files are skipped.
