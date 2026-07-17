import yaml

def replace_refs(obj):
    if isinstance(obj, dict):
        if '$ref' in obj and isinstance(obj['$ref'], str):
            ref = obj['$ref']
            if ref.startswith('../models/'):
                # ../models/Domain.yaml -> #/components/schemas/Domain
                model_name = ref.replace('../models/', '').replace('.yaml', '')
                obj['$ref'] = f'#/components/schemas/{model_name}'
        for k, v in obj.items():
            replace_refs(v)
    elif isinstance(obj, list):
        for item in obj:
            replace_refs(item)

def main():
    with open('Webhooks copy.yaml', 'r') as f:
        webhooks_data = yaml.safe_load(f)
        
    with open('Mailpool-API-3.0.yaml', 'r') as f:
        api_data = yaml.safe_load(f)

    webhook_schemas = webhooks_data.get('components', {}).get('schemas', {})
    
    # Process refs
    replace_refs(webhook_schemas)
    
    # Merge schemas
    if 'components' not in api_data:
        api_data['components'] = {}
    if 'schemas' not in api_data['components']:
        api_data['components']['schemas'] = {}
        
    for name, schema in webhook_schemas.items():
        # Capitalize the schema name (e.g. domains.registered -> DomainsRegistered)
        # oapi-codegen handles names fine, but let's make it PascalCase if we want, or just leave it.
        # It's better to leave it as is or sanitize if oapi-codegen chokes. 
        # Actually oapi-codegen does TitleCase conversion, but let's do it here just in case.
        # domains.registered -> DomainsRegistered
        clean_name = ''.join(word.title() for word in name.replace('-', '.').split('.'))
        
        # update title too just in case
        if 'title' in schema:
            schema['title'] = clean_name
            
        api_data['components']['schemas'][clean_name] = schema
        
    with open('Mailpool-API-3.0.yaml', 'w') as f:
        yaml.dump(api_data, f, sort_keys=False)

if __name__ == '__main__':
    main()
