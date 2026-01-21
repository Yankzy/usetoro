import graphene
import logging
from django.apps import apps
from django.conf import settings

logger = logging.getLogger(__name__)

queries = set()
mutations = set()

# Apps that have a schema to be loaded
# We can dynamically get this or hardcode for now based on known migrations
TARGET_APPS = ['users', 'webhookks', 'quickbooks']

for app_label in TARGET_APPS:
    try:
        # Import the schema module for the app
        schema_module = __import__(f"{app_label}.schema", fromlist=["schema"])
        schema = schema_module.schema
        
        # Check for Query class
        if hasattr(schema_module, "Query"):
            queries.add(schema_module.Query)
            logger.debug(f"{app_label} queries loaded")

        # Check for Mutation class
        if hasattr(schema_module, "Mutation"):
            mutation = schema_module.Mutation
            if mutation:
                mutations.add(mutation)
                logger.debug(f"{app_label} mutations loaded")

    except ImportError:
        logger.debug(f"{app_label} schema couldn't be loaded")
        continue
    except Exception as exc:
        logger.error(f"{app_label} encountered an exception: {exc}")
        raise

if not queries:
    class Query(graphene.ObjectType):
        pass
else:
    class Query(*queries): pass

if not mutations:
    class Mutation(graphene.ObjectType):
        pass
else:
    class Mutation(*mutations): pass

schema = graphene.Schema(query=Query, mutation=Mutation)
