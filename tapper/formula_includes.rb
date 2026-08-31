# Sawmill is one long-lived daemon shared by every MCP session, so the formula
# ships a service definition rather than leaving users to hand-roll a launchd
# plist. `brew services start sawmill` is the documented install step.
service do
  run [opt_bin/"sawmill", "serve"]
  keep_alive true
  log_path var/"log/sawmill/sawmill.log"
  error_log_path var/"log/sawmill/sawmill.log"
end
