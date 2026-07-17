import re
with open('go/internal/services/mailpool/mailpool.gen.go', 'r') as f:
    text = f.read()
matches = re.findall(r'func \(c \*ClientWithResponses\) ([a-zA-Z0-9_]+WithResponse)\(ctx context\.Context, (.*?)\)', text)
for match in matches:
    if "WithBodyWithResponse" not in match[0]:
        print(f"  - {match[0]}({match[1]})")
