import graphene
import gql.webhookks.queries
import gql.webhookks.mutations

class Query(gql.webhookks.queries.Query, graphene.ObjectType):
    pass

class Mutation(gql.webhookks.mutations.Mutation, graphene.ObjectType):
    pass

schema = graphene.Schema(query=Query, mutation=Mutation)
