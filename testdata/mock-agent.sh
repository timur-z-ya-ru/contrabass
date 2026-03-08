#!/bin/bash
# Mock codex agent that stays alive and outputs minimal JSONL
# Reads stdin for JSON-RPC requests and responds with valid responses

while IFS= read -r line; do
  id=$(echo "$line" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
  method=$(echo "$line" | grep -o '"method":"[^"]*"' | head -1 | cut -d'"' -f4)
  
  case "$method" in
    "initialize")
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"capabilities\":{}}}"
      ;;
    *)
      # Ignore other methods, just keep alive
      ;;
  esac
done
