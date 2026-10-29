//! agent-keepalive-desktop: the agent-keepalive UI in a native window.
//!
//! The server is the `agent-keepalive` program; this window only shows the
//! page it serves on 127.0.0.1. `agent-keepalive app` (what the desktop
//! installs run by default) starts the server if needed and runs this with
//! --url. Started without --url, as the macOS app bundle is, it runs
//! `agent-keepalive open --no-browser` itself to get the URL.
//!
//! Links to other sites open in the default browser. Closing the window
//! leaves the server running; the window closes itself once the server has
//! stopped (Quit in the UI, `agent-keepalive stop`).

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::{Path, PathBuf};
use std::process::{Command, ExitCode, Stdio};
use std::time::Duration;

use tauri::webview::NewWindowResponse;
use tauri::{AppHandle, Manager, Url, WebviewUrl, WebviewWindowBuilder};

const VERSION: &str = match option_env!("AGENT_KEEPALIVE_VERSION") {
    Some(v) => v,
    None => "dev",
};

const USAGE: &str = "Usage: agent-keepalive-desktop [--url URL] [--data-dir DIR]

Shows the agent-keepalive UI in a window. Without --url it runs
`agent-keepalive open --no-browser` to start the server and find it.
Usually started by `agent-keepalive app`.
";

/// How often the server is checked, and after how many failed checks in a
/// row the window gives up on it. A self-update restarts the server, which
/// takes a few seconds; that must not close the window.
const HEALTH_INTERVAL: Duration = Duration::from_secs(2);
const HEALTH_FAILURES: u32 = 5;

#[derive(Debug, Default, PartialEq)]
struct Args {
    url: Option<String>,
    data_dir: Option<PathBuf>,
}

enum Parsed {
    Run(Args),
    Exit(&'static str),
}

fn parse_args(mut it: impl Iterator<Item = String>) -> Result<Parsed, String> {
    let mut a = Args::default();
    while let Some(arg) = it.next() {
        let (name, inline) = match arg.split_once('=') {
            Some((n, v)) if n.starts_with("--") => (n.to_string(), Some(v.to_string())),
            _ => (arg.clone(), None),
        };
        let mut value = || {
            inline
                .clone()
                .or_else(|| it.next())
                .ok_or(format!("{name} needs a value"))
        };
        match name.as_str() {
            "--url" => a.url = Some(value()?),
            "--data-dir" => a.data_dir = Some(PathBuf::from(value()?)),
            "--version" | "-version" | "version" => return Ok(Parsed::Exit("version")),
            "-h" | "--help" | "-help" | "help" => return Ok(Parsed::Exit("help")),
            // Older macOS passes a process serial number to apps it launches.
            s if s.starts_with("-psn_") => {}
            _ => return Err(format!("unknown argument {arg:?}")),
        }
    }
    Ok(Parsed::Run(a))
}

/// The server URL must be our own server: plain http on a loopback name.
fn server_url(s: &str) -> Result<Url, String> {
    let u = Url::parse(s).map_err(|e| format!("bad URL {s:?}: {e}"))?;
    let loopback = matches!(u.host_str(), Some("127.0.0.1" | "localhost" | "[::1]"));
    if u.scheme() != "http" || !loopback || u.port().is_none() {
        return Err(format!(
            "{s} is not an agent-keepalive server on this machine"
        ));
    }
    Ok(u)
}

fn main() -> ExitCode {
    let args = match parse_args(std::env::args().skip(1)) {
        Ok(Parsed::Run(a)) => a,
        Ok(Parsed::Exit("version")) => {
            println!("agent-keepalive-desktop {VERSION}");
            return ExitCode::SUCCESS;
        }
        Ok(Parsed::Exit(_)) => {
            print!("{USAGE}");
            return ExitCode::SUCCESS;
        }
        Err(e) => {
            eprintln!("agent-keepalive-desktop: {e}\n\n{USAGE}");
            return ExitCode::from(2);
        }
    };
    let url = match args
        .url
        .clone()
        .map_or_else(|| discover(args.data_dir.as_deref()), Ok)
    {
        Ok(u) => u,
        Err(e) => {
            eprintln!("agent-keepalive-desktop: {e}");
            return ExitCode::FAILURE;
        }
    };
    let url = match server_url(&url) {
        Ok(u) => u,
        Err(e) => {
            eprintln!("agent-keepalive-desktop: {e}");
            return ExitCode::from(2);
        }
    };
    if let Err(e) = run(url, args.data_dir) {
        eprintln!("agent-keepalive-desktop: {e}");
        return ExitCode::FAILURE;
    }
    ExitCode::SUCCESS
}

fn run(url: Url, data_dir: Option<PathBuf>) -> tauri::Result<()> {
    tauri::Builder::default()
        // A second start (the menu entry clicked again) shows this window
        // and exits.
        .plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
            show(app)
        }))
        .setup(move |app| {
            let origin = url.origin();
            let nav_origin = origin.clone();
            #[allow(unused_mut)]
            let mut b = WebviewWindowBuilder::new(app, "main", WebviewUrl::External(url.clone()))
                .title("agent-keepalive")
                .inner_size(1280.0, 860.0)
                .min_inner_size(480.0, 400.0)
                .on_navigation(move |u| {
                    if u.origin() == nav_origin || u.as_str() == "about:blank" {
                        return true;
                    }
                    open_external(u);
                    false
                })
                .on_new_window(move |u, _| {
                    open_external(&u);
                    NewWindowResponse::Deny
                });
            // The webview's storage (theme, pinned projects) lives with the
            // rest of the state, so a portable install stays in its folder.
            // macOS has no such setting; WKWebView keeps it per app.
            #[cfg(not(target_os = "macos"))]
            if let Some(d) = &data_dir {
                b = b.data_directory(d.join("webview"));
            }
            #[cfg(target_os = "macos")]
            let _ = &data_dir;
            b.build()?;

            let handle = app.handle().clone();
            std::thread::spawn(move || watch(handle, url));
            Ok(())
        })
        .run(tauri::generate_context!())
}

fn show(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.unminimize();
        let _ = w.show();
        let _ = w.set_focus();
    }
}

/// Close the window once the server has stopped answering.
fn watch(app: AppHandle, base: Url) {
    let mut failed = 0;
    loop {
        std::thread::sleep(HEALTH_INTERVAL);
        if healthy(&base) {
            failed = 0;
            continue;
        }
        failed += 1;
        if failed >= HEALTH_FAILURES {
            app.exit(0);
            return;
        }
    }
}

/// GET api/health, as plain HTTP/1.0 over a socket: the server is on this
/// machine and answers in a line, so this needs no HTTP client.
fn healthy(base: &Url) -> bool {
    let Ok(target) = base.join("api/health") else {
        return false;
    };
    let Some(addr) = base
        .socket_addrs(|| None)
        .ok()
        .and_then(|a| a.into_iter().next())
    else {
        return false;
    };
    let Ok(mut conn) = TcpStream::connect_timeout(&addr, Duration::from_secs(2)) else {
        return false;
    };
    let _ = conn.set_read_timeout(Some(Duration::from_secs(3)));
    let _ = conn.set_write_timeout(Some(Duration::from_secs(3)));
    let host = format!("{}:{}", base.host_str().unwrap_or("127.0.0.1"), addr.port());
    let req = format!(
        "GET {} HTTP/1.0\r\nHost: {host}\r\nConnection: close\r\n\r\n",
        target.path()
    );
    if conn.write_all(req.as_bytes()).is_err() {
        return false;
    }
    let mut head = [0u8; 16];
    let mut n = 0;
    while n < head.len() {
        match conn.read(&mut head[n..]) {
            Ok(0) | Err(_) => break,
            Ok(k) => n += k,
        }
    }
    status_ok(&head[..n])
}

fn status_ok(head: &[u8]) -> bool {
    head.starts_with(b"HTTP/1.") && head.get(8..13) == Some(b" 200 ".as_slice())
}

/// Open a link in the default browser. Only web links: the page decides
/// what it links to, and nothing else should start a program.
fn open_external(u: &Url) {
    if !matches!(u.scheme(), "http" | "https") {
        return;
    }
    let s = u.as_str();
    let mut cmd = if cfg!(windows) {
        let mut c = Command::new("rundll32");
        c.args(["url.dll,FileProtocolHandler", s]);
        c
    } else if cfg!(target_os = "macos") {
        let mut c = Command::new("open");
        c.arg(s);
        c
    } else {
        let mut c = Command::new("xdg-open");
        c.arg(s);
        c
    };
    cmd.stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    if let Ok(mut child) = cmd.spawn() {
        std::thread::spawn(move || child.wait());
    }
}

/// Start the server if it is not running and return its URL, by running
/// `agent-keepalive open --no-browser`, which prints it.
fn discover(data_dir: Option<&Path>) -> Result<String, String> {
    let bin = server_binary()
        .ok_or("agent-keepalive is not installed (set AGENT_KEEPALIVE_BIN to it)")?;
    let mut cmd = Command::new(&bin);
    cmd.args(["open", "--no-browser"]);
    if let Some(d) = data_dir {
        cmd.arg("--data-dir").arg(d);
    }
    cmd.stdin(Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    let out = cmd
        .output()
        .map_err(|e| format!("run {}: {e}", bin.display()))?;
    if !out.status.success() {
        return Err(format!(
            "{} open: {}",
            bin.display(),
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .map(str::trim)
        .rfind(|l| !l.is_empty())
        .map(str::to_string)
        .ok_or_else(|| format!("{} open printed no URL", bin.display()))
}

/// Where the agent-keepalive program is: $AGENT_KEEPALIVE_BIN, next to this
/// one (Windows, AppImage), the Linux packages' /usr/bin from
/// /usr/lib/agent-keepalive, the macOS package's /usr/local/bin, then $PATH.
fn server_binary() -> Option<PathBuf> {
    if let Some(p) = std::env::var_os("AGENT_KEEPALIVE_BIN") {
        return Some(PathBuf::from(p));
    }
    let name = if cfg!(windows) {
        "agent-keepalive.exe"
    } else {
        "agent-keepalive"
    };
    let mut candidates = Vec::new();
    if let Some(dir) = std::env::current_exe()
        .ok()
        .and_then(|e| e.parent().map(Path::to_path_buf))
    {
        candidates.push(dir.join(name));
        candidates.push(dir.join("../../bin").join(name));
    }
    for d in ["/usr/local/bin", "/opt/homebrew/bin", "/usr/bin"] {
        candidates.push(Path::new(d).join(name));
    }
    if let Some(path) = std::env::var_os("PATH") {
        candidates.extend(std::env::split_paths(&path).map(|d| d.join(name)));
    }
    candidates.into_iter().find(|p| p.is_file())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(args: &[&str]) -> Result<Parsed, String> {
        parse_args(args.iter().map(|s| s.to_string()))
    }

    #[test]
    fn args() {
        let Ok(Parsed::Run(a)) = parse(&["--url", "http://127.0.0.1:4344/", "--data-dir=/tmp/d"])
        else {
            panic!("did not parse");
        };
        assert_eq!(a.url.as_deref(), Some("http://127.0.0.1:4344/"));
        assert_eq!(a.data_dir, Some(PathBuf::from("/tmp/d")));
        assert!(matches!(parse(&["-psn_0_12345"]), Ok(Parsed::Run(_))));
        assert!(matches!(parse(&["--version"]), Ok(Parsed::Exit("version"))));
        assert!(parse(&["--url"]).is_err());
        assert!(parse(&["--nope"]).is_err());
    }

    #[test]
    fn only_local_servers() {
        assert!(server_url("http://127.0.0.1:4344/").is_ok());
        assert!(server_url("http://localhost:4344/").is_ok());
        assert!(server_url("http://[::1]:4344/").is_ok());
        assert!(server_url("https://127.0.0.1:4344/").is_err());
        assert!(server_url("http://example.com:4344/").is_err());
        assert!(server_url("http://127.0.0.1/").is_err());
    }

    #[test]
    fn status_line() {
        assert!(status_ok(b"HTTP/1.1 200 OK\r\n"));
        assert!(status_ok(b"HTTP/1.0 200 OK"));
        assert!(!status_ok(b"HTTP/1.1 503 Service"));
        assert!(!status_ok(b"HTTP/1.1 20"));
    }
}
