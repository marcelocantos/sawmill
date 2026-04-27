class Sawmill < Formula
  desc "MCP server for AST-level multi-language code transformations"
  homepage "https://github.com/marcelocantos/sawmill"
  license "Apache-2.0"
  # version and url will be filled in by the release process

  depends_on "go" => :build

  # Runtime tools the daemon shells out to.
  # Toolchain-coupled tools that live outside Homebrew (rustfmt and
  # rust-analyzer in ~/.cargo/bin via rustup; gopls in ~/go/bin via
  # `go install`; gofmt with the Go toolchain) are resolved through
  # the service block's PATH below.
  depends_on "gh"                          # mcp/multi_root_pr.go: PR creation
  depends_on "llvm"                        # adapters/cpp.go: clang-format + clangd
  depends_on "prettier"                    # adapters/typescript.go: formatter
  depends_on "pyright"                     # adapters/python.go: pyright-langserver
  depends_on "ruff"                        # adapters/python.go: formatter
  depends_on "typescript-language-server"  # adapters/typescript.go: LSP

  def install
    cd "go" do
      system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/sawmill"
    end
  end

  service do
    run [opt_bin/"sawmill", "serve", "--addr", "127.0.0.1:8765"]
    # launchd-spawned daemons inherit a minimal PATH (/usr/bin:/bin:...).
    # Augment it so the daemon resolves Homebrew bins plus Cargo/Go
    # and ~/.local|.py/bin user-scope tooling without requiring a
    # global `launchctl setenv PATH`.
    daemon_path = [
      "/opt/homebrew/bin", "/opt/homebrew/sbin",
      "#{Dir.home}/.cargo/bin", "#{Dir.home}/.local/bin",
      "#{Dir.home}/.py/bin", "#{Dir.home}/go/bin",
      "/usr/bin", "/bin", "/usr/sbin", "/sbin"
    ].join(":")
    environment_variables PATH: daemon_path
    keep_alive true
    log_path var/"log/sawmill/sawmill.log"
    error_log_path var/"log/sawmill/sawmill.log"
    working_dir HOMEBREW_PREFIX
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/sawmill version")
  end
end
