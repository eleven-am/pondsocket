from __future__ import annotations

from unittest.mock import Mock

import pytest

from pondsocket_common import BehaviorSubject, Subject


def test_subject_notifies_all_subscribers() -> None:
    s: Subject[int] = Subject()
    o1, o2 = Mock(), Mock()
    s.subscribe(o1)
    s.subscribe(o2)
    s.publish(10)
    o1.assert_called_once_with(10)
    o2.assert_called_once_with(10)


def test_subject_unsubscribe_reduces_size() -> None:
    s: Subject[int] = Subject()
    o = Mock()
    unsub = s.subscribe(o)
    assert s.size == 1
    unsub()
    assert s.size == 0


def test_subject_raises_when_subscribing_after_close() -> None:
    s: Subject[int] = Subject()
    s.close()
    with pytest.raises(RuntimeError, match="Cannot subscribe to a closed subject"):
        s.subscribe(Mock())


def test_subject_close_clears_observers() -> None:
    s: Subject[int] = Subject()
    s.subscribe(Mock())
    s.subscribe(Mock())
    assert s.size == 2
    s.close()
    assert s.size == 0


def test_subject_does_not_notify_after_close() -> None:
    s: Subject[int] = Subject()
    o = Mock()
    s.subscribe(o)
    s.close()
    s.publish(42)
    o.assert_not_called()


def test_subject_publishing_with_no_subscribers_is_safe() -> None:
    s: Subject[int] = Subject()
    s.publish(10)


def test_subject_double_unsubscribe_is_safe() -> None:
    s: Subject[int] = Subject()
    o = Mock()
    unsub = s.subscribe(o)
    unsub()
    unsub()
    assert s.size == 0


def test_subject_size_tracks_subscribe_and_unsubscribe() -> None:
    s: Subject[str] = Subject()
    assert s.size == 0
    u1 = s.subscribe(Mock())
    assert s.size == 1
    u2 = s.subscribe(Mock())
    assert s.size == 2
    u1()
    assert s.size == 1
    u2()
    assert s.size == 0


def test_behavior_subject_publishes_to_existing_subscribers() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject(0)
    o = Mock()
    bs.subscribe(o)
    bs.publish(10)
    assert o.call_args_list[0].args == (0,)
    assert o.call_args_list[1].args == (10,)


def test_behavior_subject_notifies_new_subscribers_with_last_value() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject(0)
    bs.publish(10)
    later = Mock()
    bs.subscribe(later)
    later.assert_called_once_with(10)


def test_behavior_subject_value_property_tracks_latest() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject(0)
    assert bs.value == 0
    bs.publish(5)
    bs.publish(10)
    bs.publish(15)
    assert bs.value == 15


def test_behavior_subject_no_initial_value_is_none_and_silent() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject()
    assert bs.value is None
    assert bs.has_value is False
    o = Mock()
    bs.subscribe(o)
    o.assert_not_called()


def test_behavior_subject_initial_value_zero_is_still_replayed() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject(0)
    o = Mock()
    bs.subscribe(o)
    o.assert_called_once_with(0)


def test_behavior_subject_close_blocks_further_subscriptions() -> None:
    bs: BehaviorSubject[int] = BehaviorSubject(0)
    bs.close()
    with pytest.raises(RuntimeError, match="Cannot subscribe to a closed subject"):
        bs.subscribe(Mock())
