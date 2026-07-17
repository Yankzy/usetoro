import yaml

with open('Webhooks copy.yaml', 'r') as f:
    webhooks_data = yaml.safe_load(f)

schemas = list(webhooks_data.get('components', {}).get('schemas', {}).keys())

with open('Mailpool-API-3.0.yaml', 'r') as f:
    api_data = yaml.safe_load(f)

dummy_refs = []
for name in schemas:
    clean_name = ''.join(word.title() for word in name.replace('-', '.').split('.'))
    dummy_refs.append({'$ref': f'#/components/schemas/{clean_name}'})

if 'paths' not in api_data:
    api_data['paths'] = {}

api_data['paths']['/dummy-webhooks'] = {
    'post': {
        'summary': 'Dummy endpoint to prevent pruning of webhook schemas',
        'operationId': 'ReceiveWebhook',
        'requestBody': {
            'content': {
                'application/json': {
                    'schema': {
                        'oneOf': dummy_refs
                    }
                }
            }
        },
        'responses': {
            '200': {
                'description': 'OK'
            }
        }
    }
}

with open('Mailpool-API-3.0.yaml', 'w') as f:
    yaml.dump(api_data, f, sort_keys=False)

