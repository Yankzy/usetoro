import graphene
import gql.users.queries
import gql.users.mutations

class Query(gql.users.queries.Query, graphene.ObjectType):
    pass

class Mutation(gql.users.mutations.Mutation, graphene.ObjectType):
    pass

schema = graphene.Schema(query=Query, mutation=Mutation)
