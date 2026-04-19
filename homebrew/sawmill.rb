class Sawmill < Formula
  desc "MCP server for AST-level multi-language code transformations"
  homepage "https://github.com/marcelocantos/sawmill"
  license "Apache-2.0"
  # version and url will be filled in by the release process

  depends_on "go" => :build

  def install
    cd "go" do
      system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/sawmill"
    end
  end

  service do
    run [opt_bin/"sawmill", "serve", "--addr", "127.0.0.1:8765"]
    keep_alive true
    log_path var/"log/sawmill/sawmill.log"
    error_log_path var/"log/sawmill/sawmill.log"
    working_dir HOMEBREW_PREFIX
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/sawmill version")
  end
end
