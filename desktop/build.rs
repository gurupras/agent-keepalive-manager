fn main() {
    // The release version, from scripts/build-desktop.sh.
    println!("cargo:rerun-if-env-changed=AGENT_KEEPALIVE_VERSION");
    tauri_build::build()
}
