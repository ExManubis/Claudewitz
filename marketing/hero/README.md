# Hero video

A 16-second seamless loop for the marketing site, 1920×1080 at 30 fps, no audio.

1. **0–3s:** the headphone bean bops to the beat beside the CLAUDEWITZ wordmark. The dot grid in
   the background ripples on every beat.
2. **3–6s:** the logo bursts into a swirl of sparks, which fly out and build a terminal window.
3. **6–12s:** a claudewitz session plays out in the window. A prompt is typed, Claude thinks and
   streams a reply, and the Read, Edit (with a diff) and Bash tool calls run. Meanwhile the sidebar's
   meters, todos, git status and spark mascot react, the transcript scrolls, a "Turn finished" toast
   slides in and the next prompt is suggested.
4. **12–16s:** the window dissolves and the sparks spring back into the logo with a flash. The
   tagline then types itself back in, and the last frame leads straight into the first.

| File | Use |
| --- | --- |
| `hero.webm` | VP9, for browsers that support it |
| `hero.mp4` | H.264 1080p, for Safari and anything else |
| `hero-720.mp4` | H.264 720p, for phones |
| `hero-poster.jpg` | First frame, shown while the video loads |

```html
<video autoplay muted loop playsinline preload="auto" poster="hero-poster.jpg"
       style="width:100%;height:auto;background:#141210">
  <source src="hero-720.mp4" type="video/mp4" media="(max-width: 800px)">
  <source src="hero.webm" type="video/webm">
  <source src="hero.mp4" type="video/mp4">
</video>
```

The background is `#141210`, the same as the dark logo. If you use that as the page background,
the video's edges blend in. Browsers only autoplay video that is `muted` and `playsinline`.

## Editing and re-rendering

`hero.html` draws the whole animation as a function of time. The bean is a port of the ray-marched
mascot in `internal/tui/bean.go`, the wordmark uses the glyphs from `internal/tui/fx.go`, and the
colours come from the `claude` theme. Open the page in a browser for a live preview, or add `?t=9`
to hold a single frame. The timeline constants (`T_DIS`, `T_UI` and the others) and the session
script (`ROWS`) are near the top.

```sh
npm i -D playwright            # or use a global install via NODE_PATH
node render.js --stills 0,4,9  # PNG stills in stills/, for quick checks
node render.js                 # every frame, then encode (needs ffmpeg with x264 and vpx)
node render.js --encode        # re-encode from the last render, e.g. to change quality
```

JetBrains Mono (`fonts/`) is under the SIL Open Font License.
