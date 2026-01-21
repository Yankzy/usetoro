import graphene
import gql.quickbooks.mutations

# QuickBooks only has mutations currently
class Query(graphene.ObjectType):
    pass

class Mutation(gql.quickbooks.mutations.Mutation, graphene.ObjectType):
    pass

schema = graphene.Schema(query=Query, mutation=Mutation)
