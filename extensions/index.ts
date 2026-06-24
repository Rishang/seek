import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execSync } from "node:child_process";
import { join } from "node:path";
import { platform, arch, homedir, tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { existsSync, mkdirSync, mkdtempSync, copyFileSync, chmodSync, rmSync, accessSync, constants } from "node:fs";

const __dirname = fileURLToPath(new URL(".", import.meta.url));
const PKG_ROOT = join(__dirname, "..");

const REPO = "Rishang/seek";
const INSTALL_URL = `https://raw.githubusercontent.com/${REPO}/main/install.sh`;

/** Map Node platform + arch to the suffix seek releases use. */
function targetSuffix(): string | null {
  const osName = platform();
  const archName = arch();

  let os: string;
  let a: string;

  if (osName === "linux") os = "linux";
  else if (osName === "darwin") os = "darwin";
  else return null;

  if (archName === "x64") a = "amd64";
  else if (archName === "arm64") a = "arm64";
  else return null;

  return `${os}-${a}`;
}

/** Resolve latest release tag from GitHub API. */
async function latestTag(): Promise<string | null> {
  try {
    const r = await fetch(`https://api.github.com/repos/${REPO}/releases/latest`);
    if (!r.ok) return null;
    const j = (await r.json()) as { tag_name?: string };
    return j.tag_name ?? null;
  } catch {
    return null;
  }
}

/** Returns true if `seek` is found on PATH. */
function seekAvailable(): boolean {
  try {
    execSync("command -v seek", { stdio: "pipe" });
    return true;
  } catch {
    return false;
  }
}

/**
 * Extract a tar.gz buffer into a dest dir using system tar.
 * Returns true on success.
 */
function extractTarGz(buf: Buffer, destDir: string): boolean {
  const { writeFileSync } = require("node:fs") as typeof import("node:fs");
  const tarPath = join(destDir, "seek.tar.gz");
  writeFileSync(tarPath, buf);
  try {
    execSync(`tar -xzf "${tarPath}" -C "${destDir}"`, { stdio: "pipe" });
    return true;
  } catch {
    return false;
  } finally {
    try { rmSync(tarPath); } catch {}
  }
}

/**
 * Download and install seek binary in-process (no shell dependency).
 * Returns the installed destination path, or null on failure.
 */
async function installSeek(): Promise<string | null> {
  const suffix = targetSuffix();
  if (!suffix) return null;

  const tag = await latestTag();
  if (!tag) return null;

  const asset = `seek-${tag}-${suffix}.tar.gz`;
  const binname = `seek-${suffix}`;
  const url = `https://github.com/${REPO}/releases/download/${tag}/${asset}`;

  const resp = await fetch(url);
  if (!resp.ok) return null;
  const buf = Buffer.from(await resp.arrayBuffer());

  const tmp = mkdtempSync(join(tmpdir(), "seek-"));
  try {
    if (!extractTarGz(buf, tmp)) return null;

    const binPath = join(tmp, binname);
    if (!existsSync(binPath)) return null;
    chmodSync(binPath, 0o755);

    // Pick install dir — prefer /usr/local/bin if writable.
    const homeBin = join(homedir(), ".local", "bin");
    let destDir: string;
    try {
      accessSync("/usr/local/bin", constants.W_OK);
      destDir = "/usr/local/bin";
    } catch {
      destDir = homeBin;
    }
    mkdirSync(destDir, { recursive: true });

    const dest = join(destDir, "seek");
    copyFileSync(binPath, dest);
    chmodSync(dest, 0o755);
    return dest;
  } finally {
    try { rmSync(tmp, { recursive: true }); } catch {}
  }
}

export default function (pi: ExtensionAPI) {
  // ——— Register the web-fetch skill via resources_discover ———
  const skillsPath = join(PKG_ROOT, "skills");
  pi.on("resources_discover", async (_event, _ctx) => {
    return { skillPaths: [skillsPath] };
  });

  // ——— On session start: check / install seek ———
  pi.on("session_start", async (_event, ctx) => {
    if (seekAvailable()) {
      ctx.ui.notify("web-fetch: seek ready", "info");
      return;
    }

    const suffix = targetSuffix();
    if (!suffix) {
      ctx.ui.notify(
        `seek: unsupported platform (${platform()}/${arch()}).`,
        "warn",
      );
      return;
    }

    // Non-interactive → warn and move on.
    if (!ctx.hasUI) {
      ctx.ui.notify(
        `seek not found. Install: curl -fsSL ${INSTALL_URL} | sh`,
        "warn",
      );
      return;
    }

    // Interactive → offer one-click install.
    const ok = await ctx.ui.confirm(
      "seek not found",
      "seek is the web-search CLI used by the web-fetch skill. " +
        "Install it now? (Downloads prebuilt binary from GitHub)",
    );

    if (!ok) {
      ctx.ui.notify(
        `web-fetch won't work without seek. Install: curl -fsSL ${INSTALL_URL} | sh`,
        "warn",
      );
      return;
    }

    ctx.ui.setStatus("seek", "Installing seek…");

    // Strategy 1: in-process download + extract (no curl/tar on PATH needed).
    try {
      const dest = await installSeek();
      if (dest) {
        ctx.ui.notify(`seek installed → ${dest}`, "info");
        ctx.ui.setStatus("seek", "seek ready ✓");
        return;
      }
    } catch { /* fall through to shell install */ }

    // Strategy 2: shell out to the official install.sh.
    try {
      ctx.ui.setStatus("seek", "Installing seek via shell…");
      const homeBin = join(homedir(), ".local", "bin");
      execSync(`curl -fsSL ${INSTALL_URL} | sh`, {
        stdio: "pipe",
        env: { ...process.env, SEEK_BIN_DIR: homeBin },
        timeout: 60_000,
      });
      if (seekAvailable()) {
        ctx.ui.notify("seek installed successfully", "info");
        ctx.ui.setStatus("seek", "seek ready ✓");
      } else {
        ctx.ui.notify(
          `seek installed but not on PATH. Run: curl -fsSL ${INSTALL_URL} | sh`,
          "warn",
        );
        ctx.ui.setStatus("seek", "");
      }
    } catch {
      ctx.ui.notify(
        `seek install failed. Manual: curl -fsSL ${INSTALL_URL} | sh`,
        "error",
      );
      ctx.ui.setStatus("seek", "");
    }
  });

  // ——— /seek-install command for manual (re)install ———
  pi.registerCommand("seek-install", {
    description: "Install or reinstall the seek web-search CLI",
    handler: async (_args, ctx) => {
      if (seekAvailable()) {
        const v = execSync("seek --version", { stdio: "pipe" }).toString().trim();
        ctx.ui.notify(`seek already installed: ${v}`, "info");
        const reinstall = await ctx.ui.confirm(
          "Reinstall seek?",
          "Download and reinstall the seek binary?",
        );
        if (!reinstall) return;
      }

      ctx.ui.setStatus("seek", "Installing seek…");
      try {
        execSync(`curl -fsSL ${INSTALL_URL} | sh`, {
          stdio: "inherit",
          timeout: 60_000,
        });
        ctx.ui.notify("seek installed successfully", "info");
        ctx.ui.setStatus("seek", "seek ready ✓");
      } catch {
        ctx.ui.notify(
          `Install failed. Manual: curl -fsSL ${INSTALL_URL} | sh`,
          "error",
        );
        ctx.ui.setStatus("seek", "");
      }
    },
  });
}