import re

with open("go/internal/services/mailpool/mailpool.gen.go", "r") as f:
    content = f.read()
    
match = re.search(r"type ClientWithResponsesInterface interface \{(.*?)\n\}", content, re.DOTALL)
methods = match.group(1).strip().split('\n')

test_cases = []
for line in methods:
    line = line.strip()
    if not line or line.startswith('//'):
        continue
    
    m_match = re.match(r"(\w+)\((.*?)\)", line)
    if not m_match:
        continue
        
    name = m_match.group(1)
    args_str = m_match.group(2)
    
    args = args_str.split(',')
    call_args = ["ctx"]
    for arg in args[1:]:
        arg = arg.strip()
        if "reqEditors" in arg:
            continue
        parts = arg.split(' ')
        arg_name = parts[0]
        arg_type = " ".join(parts[1:])
        
        if arg_type.startswith('*'):
            call_args.append(f"&mailpool.{arg_type[1:]}{{}}")
        elif arg_type == "string":
            call_args.append('"test"')
        elif arg_type == "float32" or arg_type == "int":
            call_args.append("1")
        elif arg_type == "io.Reader":
            call_args.append("strings.NewReader(`{}`)")
        elif arg_type.endswith("RequestBody"):
            call_args.append(f"mailpool.{arg_type}{{}}")
        else:
            # Assume it's a type alias to string (like enum)
            call_args.append(f"mailpool.{arg_type}(\"test\")")
            
    call_args_str = ", ".join(call_args)
    
    test_cases.append(f"""
	t.Run("{name}", func(t *testing.T) {{
		_, err := client.{name}({call_args_str})
		require.NoError(t, err, "Expected no error for {name}")
	}})
""")

test_file = f"""package mailpool_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/stretchr/testify/require"
)

func TestMailpoolGeneratedMethods(t *testing.T) {{
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {{
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{{}}`))
	}}))
	defer ts.Close()

	client, err := mailpool.NewMailpool(ts.URL, "test-api-key")
	require.NoError(t, err)

	ctx := context.Background()

{''.join(test_cases)}
}}
"""

with open("go/internal/services/mailpool/mailpool_gen_test.go", "w") as f:
    f.write(test_file)
