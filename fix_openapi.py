import yaml
import sys

def convert_types(obj):
    if isinstance(obj, dict):
        if 'type' in obj:
            if isinstance(obj['type'], list):
                types = obj['type']
                if 'null' in types:
                    types.remove('null')
                    obj['nullable'] = True
                if len(types) == 1:
                    obj['type'] = types[0]
                elif len(types) == 0:
                    del obj['type']
            elif obj['type'] == 'null':
                del obj['type']
                obj['nullable'] = True

        if 'oneOf' in obj and isinstance(obj['oneOf'], list):
            new_oneof = []
            for item in obj['oneOf']:
                if isinstance(item, dict) and item.get('type') == 'null':
                    obj['nullable'] = True
                else:
                    new_oneof.append(item)
            if len(new_oneof) == 1:
                obj.update(new_oneof[0])
                del obj['oneOf']
            elif len(new_oneof) > 1:
                obj['oneOf'] = new_oneof
            else:
                del obj['oneOf']
                obj['nullable'] = True

        if 'anyOf' in obj and isinstance(obj['anyOf'], list):
            new_anyof = []
            for item in obj['anyOf']:
                if isinstance(item, dict) and item.get('type') == 'null':
                    obj['nullable'] = True
                else:
                    new_anyof.append(item)
            if len(new_anyof) == 1:
                obj.update(new_anyof[0])
                del obj['anyOf']
            elif len(new_anyof) > 1:
                obj['anyOf'] = new_anyof
            else:
                del obj['anyOf']
                obj['nullable'] = True


        # recurse
        for k, v in list(obj.items()):
            convert_types(v)
    elif isinstance(obj, list):
        for item in obj:
            convert_types(item)

def main():
    with open('Mailpool-API.yaml', 'r') as f:
        data = yaml.safe_load(f)
    
    data['openapi'] = '3.0.0'
    convert_types(data)
    
    with open('Mailpool-API-3.0.yaml', 'w') as f:
        yaml.dump(data, f, sort_keys=False)

if __name__ == '__main__':
    main()
