from graphql.error import GraphQLError
import logging
from pinecone import Pinecone
import os
from ledger.models import EntityModel
from django.db import transaction
from typing import Optional, List, Dict, Any
import time
import openai  # Import openai
from pinecone import ServerlessSpec



pc = Pinecone(api_key=os.getenv("PINECONE_API_KEY"))
openai_client = openai.OpenAI(api_key=os.getenv("OPENAI_API_KEY"))

logger = logging.getLogger("error_logger")



# --- Constants ---
# This should be an integer
OPENAI_EMBEDDING_DIMENSIONS = int(os.getenv("OPENAI_EMBEDDING_DIMENSIONS", 1536))
OPENAI_EMBEDDING_MODEL = "text-embedding-3-small"
PINECONE_INDEX_NAME = "voxprofit" # New index for new model
UPSERT_BATCH_SIZE = 100 # Batch size for upserting
# Context prefix to prime the model for better semantic relevance
EMBEDDING_CONTEXT_PREFIX = "Chart of Account: " 


class EmbeddingsManager:
    _instance = None
    def __new__(cls, *args, **kwargs):
        if cls._instance is None:
            cls._instance = super().__new__(cls, *args, **kwargs)
        return cls._instance

    @staticmethod
    def get_index():
        """
        Gets or creates a standard Pinecone index configured for OpenAI embeddings.
        """
        try:
            index_name = PINECONE_INDEX_NAME
            if not pc.has_index(index_name):
                logger.info(f"Creating index '{index_name}' for OpenAI embeddings...")
                pc.create_index(
                    name=index_name,
                    vector_type="dense", 
                    dimension=OPENAI_EMBEDDING_DIMENSIONS,
                    metric="cosine",
                    spec=ServerlessSpec(
                        cloud="aws",
                        region="us-east-1"
                    ),
                    deletion_protection="disabled",
                    tags={
                        "environment": "development"
                    }   
                )
                logger.info(f"Index '{index_name}' created successfully.")
            return pc.Index(index_name)
        except Exception as oops:
            import traceback
            logger.error(f"Error in get_index method: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in get_index method: {oops}")    

    def insert_account_embedding(self, account) -> str:
        """
        Generate and insert a single AccountModel embedding into Pinecone.
        Uses the same namespace and metadata format as bulk CoA upserts.
        """
        try:
            index = self.get_index()
            if not index:
                return "index not found"

            # Build metadata consistent with bulk upsert
            full_text_content = f"{account.name} | role: {account.role} | balance_type: {account.balance_type}"

            # prime the model with the context prefix to improve semantic relevance
            text_to_embed = f"{EMBEDDING_CONTEXT_PREFIX}{account.name}"

            vector = self.get_embedding(text_to_embed)
            if not vector:
                logger.error("Failed to generate embedding for account.")
                return "embedding generation failed"

            namespace = f"coa_{account.coa_model.entity.name}"
            index.upsert(
                vectors=[{
                    "id": str(account.uuid),
                    "values": vector,
                    "metadata": {
                        "chunk_text": full_text_content,
                        "embedded_text": text_to_embed
                    }
                }],
                namespace=namespace
            )
            return "insert successful"
        except Exception as oops:
            import traceback
            logger.error(f"Error in insert_account_embedding: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in insert_account_embedding: {oops}")


    @staticmethod
    def get_embedding(text: str, model: str = OPENAI_EMBEDDING_MODEL) -> List[float]:
        """
        Generates an embedding for a single text string using the OpenAI client.
        """
        try:
            if not text or not isinstance(text, str):
                logger.warning("get_embedding received empty or invalid text.")
                return []
                
            # Replace newlines, as recommended by OpenAI
            text = text.replace("\n", " ")
                
            response = openai_client.embeddings.create(
                input=[text],
                model=model
            )
            return response.data[0].embedding
        except Exception as oops:
            import traceback
            logger.error(f"Error in get_embedding method: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in get_embedding method: {oops}")

    @staticmethod
    def get_embeddings_batch(texts: List[str], model: str = OPENAI_EMBEDDING_MODEL) -> List[List[float]]:
        """
        Generates embeddings for a batch of texts using the OpenAI client.
        """
        try:
            if not texts:
                return []
            
            # Replace newlines
            texts = [t.replace("\n", " ") for t in texts]

            response = openai_client.embeddings.create(
                input=texts,
                model=model
            )
            return [data.embedding for data in response.data]
        except Exception as oops:
            import traceback
            logger.error(f"Error in get_embeddings_batch method: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in get_embeddings_batch method: {oops}")



    @staticmethod
    def _fetch_data_sync(entity_uuid: str) -> Optional[List[Dict[str, Any]]]:
        """
        A synchronous helper function to fetch and prepare all required data
        from the database.
        
        MODIFIED: Now adds a prefix to the embedded text for better context.
        """
        try:
            with transaction.atomic():
                entity: EntityModel = EntityModel.objects.get(uuid=entity_uuid)
                accounts = list(entity.get_all_accounts(active=True))
                
                accounts_data = []
                for i, a in enumerate(accounts):
                    # This is the full string we want to see in results
                    full_text_content = f"{a.name} | role: {a.role} | balance_type: {a.balance_type}"
                    
                    # Add prefix to prime the model for better semantic comparison
                    text_to_embed = f"{EMBEDDING_CONTEXT_PREFIX}{a.name}"

                    accounts_data.append({
                        "id": str(i + 1),
                        # This metadata will be stored in Pinecone
                        "metadata": {
                            "chunk_text": full_text_content, # The full string for display
                            "embedded_text": text_to_embed    # Primed text for embedding
                        }
                    })
                return accounts_data, entity.name
        except EntityModel.DoesNotExist:
            logger.error(f"EntityModel with uuid {entity_uuid} does not exist.")
            raise
        except Exception as oops:
            import traceback
            logger.error(f"Error in _fetch_data_sync method: {oops}")
            traceback.print_exc()
            # Re-raise the exception to be handled by the caller
            raise

    def upsert_coa(self, entity_uuid: str):
        """
        Fetches data, generates OpenAI embeddings, and upserts vectors
        to the standard Pinecone index.
        
        """
        try:
            dense_index = self.get_index()
            if not dense_index:
                return "index not found"

            # 1. Fetch data (IDs and metadata)
            data_to_prepare, entity_name = self._fetch_data_sync(entity_uuid)
            if not data_to_prepare:
                logger.warning(f"No data fetched for entity_uuid {entity_uuid}. Nothing to upsert.")
                return "No data to upsert"
                
            logger.info(f"Fetched {len(data_to_prepare)} accounts for entity {entity_name}. Generating embeddings...")

            namespace = f"coa_{entity_name}"

            # Process in batches
            for i in range(0, len(data_to_prepare), UPSERT_BATCH_SIZE):
                batch_data = data_to_prepare[i : i + UPSERT_BATCH_SIZE]
                
                # 2. Get batch of texts to embed
                # We now pull the text from 'embedded_text' (which has the prefix)
                texts_to_embed = [item["metadata"]["embedded_text"] for item in batch_data]
                
                # 3. Generate embeddings
                vectors = self.get_embeddings_batch(texts_to_embed)
                
                if len(vectors) != len(batch_data):
                    raise Exception("Mismatch between number of texts and number of vectors returned.")

                # 4. Prepare vectors for upsert
                vectors_to_upsert = []
                for j, item in enumerate(batch_data):
                    vectors_to_upsert.append({
                        "id": item["id"],
                        "values": vectors[j], # The OpenAI embedding
                        "metadata": item["metadata"] # Contains both chunk_text and embedded_text
                    })
                
                # 5. Upsert the batch
                logger.info(f"Upserting batch {i // UPSERT_BATCH_SIZE + 1} with {len(vectors_to_upsert)} vectors to namespace '{namespace}'...")
                dense_index.upsert(vectors=vectors_to_upsert, namespace=namespace)

            # Wait for 10 seconds to allow the index to be updated
            time.sleep(10)
            stats = dense_index.describe_index_stats()
            logger.info(f"Upsert complete. Stats for the index: {stats} for entity: {entity_name}")

            return "upsert successful"
        except Exception as oops:
            import traceback
            logger.error(f"Error in upsert_coa method: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in upsert_coa method: {oops}")

    

    def get_coa_context_by_vector(self, entity_name: str, text_to_search: str):
        """
        Returns a list of objects with id, score, and chunk_text
        using a direct vector query (index.query) and cosine similarity.
        This now uses the OpenAI embedding.
        
        MODIFIED: Now adds the *same context prefix* to the query text.
        """
        try:
            index = self.get_index()
            
            # 1. Generate the OpenAI embedding locally
            
            # --- MODIFICATION ---
            # Extract only the name part (before the "|")
            query_name_only = text_to_search.split('|')[0].strip()
            
            # Add the *exact same prefix* we used during upsert
            prefixed_query = f"{EMBEDDING_CONTEXT_PREFIX}{query_name_only}"
            
            logger.info(f"Original query: '{text_to_search}', Embedded query: '{prefixed_query}'")

            vector = self.get_embedding(prefixed_query)
            if not vector:
                logger.error("Failed to generate embedding for query.")
                return []
            
            # 2. Query Pinecone with the vector
            results = index.query(
                namespace=f"coa_{entity_name}",
                vector=vector,
                top_k=5,
                include_values=False,
                include_metadata=True  # Retrieve the metadata we stored
            )

            # 3. Format the results
            # The score will be a 0.0-1.0 cosine similarity
            hits = results.get("matches", [])
            return [
                {
                    "id": hit.get("id"),
                    "similarity_score": hit.get("score"),
                    # Retrieve the *full* display text from the metadata
                    "chunk_text": (hit.get("metadata") or {}).get("chunk_text"),
                }
                for hit in hits
            ]
        except Exception as oops:
            import traceback
            logger.error(f"Error in get_coa_context_by_vector method: {oops}")
            traceback.print_exc()
            raise GraphQLError(f"Error in get_coa_context_by_vector method: {oops}")


# singleton pattern for the embeddings manager
embeddings_manager = EmbeddingsManager()