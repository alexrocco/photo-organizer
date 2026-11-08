# Photo Organizer

Simple app to organize images, copying files from source to the destination with a folder and file name convetion.

Convetion:
```
/<year>/<month>/<year>-<month>-<day>-<Hour><minute><seconds>-<EXIF camera model>-<counter>.(jpg|arw|mp4)

e.g. /2013/10/2013-10-05-030421-nexf3-00.jpg
```

GoPro `.mp4` videos are imported when `-video-dest-dir` is set (and skipped otherwise). They
keep their original camera name (e.g. `GX010182.MP4`) and go flat into that directory; a
different clip already holding the name is saved as `GX010182-01.MP4`. Only GoPro MP4s are
accepted (identified by their GPMF metadata), and GoPro `.lrv`/`.thm` preview files are skipped.
