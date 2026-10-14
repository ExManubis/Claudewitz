// Renders hero.html frame by frame in headless Chromium and encodes the loop.
//
//   node render.js                 # hero.mp4, hero.webm and hero-poster.jpg
//   node render.js --stills 0,6,9  # just PNG stills at those times, for checking
//   node render.js --encode        # re-encode from the last render's lossless master
//
// Needs Playwright (with a Chromium) and ffmpeg with libx264 and libvpx-vp9.

const { chromium } = require("playwright");
const { spawn } = require("child_process");
const path = require("path");
const fs = require("fs");

const here = __dirname;
const out = (...p) => path.join(here, ...p);

function ffmpeg(args) {
  const p = spawn("ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", ...args], { stdio: ["pipe", "inherit", "inherit"] });
  const done = new Promise((ok, fail) => p.on("close", c => (c === 0 ? ok() : fail(new Error(`ffmpeg exited ${c}`)))));
  return { stdin: p.stdin, done };
}

(async () => {
  const stillsArg = process.argv.indexOf("--stills");
  const master = out("stills", "master.mkv");
  if (process.argv.includes("--encode")) return encode(master);
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined });
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  await page.goto("file://" + out("hero.html") + "?render");
  await page.evaluate(() => window.ready);
  const { FPS, DUR } = await page.evaluate(() => window.HERO);
  const grab = async t => {
    await page.evaluate(t => window.renderFrame(t), t);
    return page.locator("#c").screenshot({ type: "png" });
  };

  if (stillsArg > 0) {
    fs.mkdirSync(out("stills"), { recursive: true });
    for (const t of process.argv[stillsArg + 1].split(",").map(Number)) {
      fs.writeFileSync(out("stills", `t${t.toFixed(2)}.png`), await grab(t));
      console.log("still", t);
    }
    await browser.close();
    return;
  }

  // Frames go to a lossless intermediate so both encodes see the same pixels.
  const n = Math.round(FPS * DUR);
  fs.mkdirSync(out("stills"), { recursive: true });
  const enc = ffmpeg(["-f", "image2pipe", "-framerate", String(FPS), "-c:v", "png", "-i", "-", "-c:v", "ffv1", master]);
  for (let f = 0; f < n; f++) {
    const buf = await grab(f / FPS);
    if (!enc.stdin.write(buf)) await new Promise(r => enc.stdin.once("drain", r));
    if (f % 30 === 0) console.log(`frame ${f}/${n}`);
  }
  enc.stdin.end();
  await enc.done;
  await browser.close();

  await encode(master);
})().catch(e => { console.error(e); process.exit(1); });

// encode makes the web versions from the master, which stays in stills/
// (ignored by git) so settings can be tuned without rendering again.
async function encode(master) {
  const x264 = ["-c:v", "libx264", "-preset", "veryslow", "-pix_fmt", "yuv420p", "-profile:v", "high", "-movflags", "+faststart", "-an"];
  console.log("encoding mp4");
  await ffmpeg(["-i", master, ...x264, "-crf", "23", out("hero.mp4")]).done;
  await ffmpeg(["-i", master, "-vf", "scale=1280:-2:flags=lanczos", ...x264, "-crf", "24", out("hero-720.mp4")]).done;
  console.log("encoding webm");
  await ffmpeg(["-i", master, "-c:v", "libvpx-vp9", "-b:v", "0", "-crf", "37", "-row-mt", "1", "-pix_fmt", "yuv420p",
    "-an", out("hero.webm")]).done;
  await ffmpeg(["-i", master, "-frames:v", "1", "-q:v", "3", out("hero-poster.jpg")]).done;
  console.log("done");
}
