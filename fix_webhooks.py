import yaml

with open('Webhooks.yaml', 'r') as f:
    data = yaml.safe_load(f)

print(list(data.get('components', {}).get('schemas', {}).keys()))
