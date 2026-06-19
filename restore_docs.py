import os
import json
import shutil
import urllib.parse

def restore_docs():
    history_paths = [
        os.path.expanduser("~/Library/Application Support/Cursor/User/History/"),
        os.path.expanduser("~/Library/Application Support/Code/User/History/"),
        os.path.expanduser("~/Library/Application Support/Antigravity IDE/User/History/")
    ]
    
    target_prefix = "file:///Users/Yankz/programming/usetoro/docs/"
    restored_count = 0
    
    for base_path in history_paths:
        if not os.path.exists(base_path):
            continue
        print(f"Scanning history directory: {base_path}...")
        
        for root, dirs, files in os.walk(base_path):
            if "entries.json" in files:
                entries_file = os.path.join(root, "entries.json")
                try:
                    with open(entries_file, "r") as f:
                        data = json.load(f)
                    
                    resource_uri = data.get("resource", "")
                    if resource_uri.startswith(target_prefix):
                        # Decode URL characters like %28 and %29
                        decoded_uri = urllib.parse.unquote(resource_uri)
                        target_file_path = decoded_uri.replace("file://", "")
                        
                        entries = data.get("entries", [])
                        if not entries:
                            continue
                        
                        # Sort by timestamp to get the latest version
                        latest_entry = max(entries, key=lambda e: e.get("timestamp", 0))
                        entry_id = latest_entry.get("id")
                        
                        source_file = os.path.join(root, entry_id)
                        if os.path.exists(source_file):
                            # Ensure target directories exist
                            os.makedirs(os.path.dirname(target_file_path), exist_ok=True)
                            
                            # Copy file back to docs/
                            shutil.copy2(source_file, target_file_path)
                            print(f"Restored: {target_file_path} (from {source_file})")
                            restored_count += 1
                        else:
                            print(f"Warning: Source entry file {source_file} not found for {target_file_path}")
                except Exception as e:
                    print(f"Error reading {entries_file}: {e}")
                    
    print(f"\nCompleted! Restored {restored_count} files successfully.")

if __name__ == "__main__":
    restore_docs()
