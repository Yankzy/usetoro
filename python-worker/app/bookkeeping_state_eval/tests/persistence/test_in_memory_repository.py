from __future__ import annotations

from threading import Event, Thread

import pytest

from bookkeeping_state_eval.persistence.in_memory import InMemoryBookkeepingRepository
from bookkeeping_state_eval.persistence.repository import (
    BookkeepingSnapshot,
    PersistenceConflictError,
    PersistenceDuplicateError,
    PersistenceError,
    PersistenceWriteSet,
)
from tests.factories import account, book_item, snapshot


def _repository() -> InMemoryBookkeepingRepository:
    return InMemoryBookkeepingRepository(initial_snapshots=[snapshot(bank_accounts=(account(),))])


def test_snapshot_is_consistent_and_artifacts_have_deterministic_order() -> None:
    repository = InMemoryBookkeepingRepository(
        initial_snapshots=[snapshot(book_items=(book_item("book-b"), book_item("book-a")))]
    )
    loaded = repository.load_snapshot(company_id="atlas")

    assert loaded.persistence_revision == 0
    assert tuple(item.id for item in loaded.book_items) == ("book-a", "book-b")
    assert repository.load_snapshot(company_id="atlas") == loaded


def test_exact_replay_is_idempotent_but_non_empty_new_commit_advances_once() -> None:
    repository = _repository()
    new_account = account("account-2")
    first = repository.commit(
        company_id="atlas", expected_revision=0,
        write_set=PersistenceWriteSet(bank_accounts=(new_account,)),
    )
    replay = repository.commit(
        company_id="atlas", expected_revision=1,
        write_set=PersistenceWriteSet(bank_accounts=(new_account,)),
    )

    assert (first.previous_revision, first.new_revision) == (0, 1)
    # A non-empty successful write set advances durable reality once, even if
    # it is an idempotent immutable replay.
    assert (replay.previous_revision, replay.new_revision) == (1, 2)
    assert tuple(item.id for item in repository.load_snapshot(company_id="atlas").bank_accounts) == ("account-1", "account-2")


def test_empty_write_set_does_not_advance_and_stale_revision_is_rejected() -> None:
    repository = _repository()
    noop = repository.commit(company_id="atlas", expected_revision=0, write_set=PersistenceWriteSet())
    assert (noop.previous_revision, noop.new_revision) == (0, 0)

    repository.commit(company_id="atlas", expected_revision=0, write_set=PersistenceWriteSet(book_items=(book_item(),)))
    with pytest.raises(PersistenceConflictError):
        repository.commit(company_id="atlas", expected_revision=0, write_set=PersistenceWriteSet())


def test_conflicting_ids_and_failed_multi_artifact_commit_leave_every_collection_unchanged() -> None:
    repository = _repository()
    before = repository.load_snapshot(company_id="atlas")
    conflicting = account().model_copy(update={"name": "different"})
    with pytest.raises(PersistenceDuplicateError):
        repository.commit(
            company_id="atlas",
            expected_revision=0,
            write_set=PersistenceWriteSet(
                book_items=(book_item(),),
                bank_accounts=(conflicting,),
            ),
        )

    assert repository.load_snapshot(company_id="atlas") == before


def test_duplicate_conflicts_in_one_write_set_and_seed_are_rejected() -> None:
    repository = _repository()
    duplicate = account("account-2")
    conflicting = duplicate.model_copy(update={"name": "different"})
    with pytest.raises(PersistenceDuplicateError):
        repository.commit(
            company_id="atlas",
            expected_revision=0,
            write_set=PersistenceWriteSet(bank_accounts=(duplicate, conflicting)),
        )

    with pytest.raises(PersistenceDuplicateError):
        InMemoryBookkeepingRepository(
            initial_snapshots=[snapshot(bank_accounts=(account(), account()))]
        )


def test_unknown_company_is_rejected() -> None:
    repository = _repository()
    with pytest.raises(PersistenceError):
        repository.load_snapshot(company_id="unknown")
    with pytest.raises(PersistenceError):
        repository.commit(company_id="unknown", expected_revision=0, write_set=PersistenceWriteSet())


def test_concurrent_reader_observes_only_before_or_after_atomic_multi_artifact_commit() -> None:
    repository = _repository()
    started = Event()
    finished = Event()
    observed: list[tuple[str, ...]] = []

    def writer() -> None:
        started.set()
        repository.commit(
            company_id="atlas",
            expected_revision=0,
            write_set=PersistenceWriteSet(book_items=(book_item("book-1"), book_item("book-2"))),
        )
        finished.set()

    def reader() -> None:
        started.wait()
        while not finished.is_set():
            observed.append(tuple(item.id for item in repository.load_snapshot(company_id="atlas").book_items))
        observed.append(tuple(item.id for item in repository.load_snapshot(company_id="atlas").book_items))

    writer_thread = Thread(target=writer)
    reader_thread = Thread(target=reader)
    reader_thread.start()
    writer_thread.start()
    writer_thread.join()
    reader_thread.join()

    assert observed
    assert set(observed) <= {(), ("book-1", "book-2")}
