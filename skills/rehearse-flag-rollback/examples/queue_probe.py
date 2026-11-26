#!/usr/bin/env python3
"""A synthetic queue migration. Each replay starts with a fresh database."""
import json
import os
import sqlite3
import sys
from urllib.request import Request, urlopen
from uuid import uuid4


def assignment():
    req = Request(os.environ["REHEARSAL_SDK_URL"] + "/api/v1/flags/",
                  headers={"X-Environment-Key": os.environ["REHEARSAL_SDK_KEY"]})
    with urlopen(req, timeout=5) as res:
        flags = json.load(res)
    return next(row["enabled"] for row in flags if row["feature"]["name"] == os.environ["REHEARSAL_FLAG"])


def job(enabled):
    return {"id": str(uuid4()), "payload": {"units": 7} if enabled else 7}


def replay(plan, pending, fresh):
    if plan not in {"immediate", "drain"}:
        raise ValueError("unknown schedule")
    db = sqlite3.connect(":memory:")
    db.execute("create table deliveries(job text primary key, units integer)")

    def deliver(item, v2_reader):
        units = item["payload"]
        if isinstance(units, dict):
            if not v2_reader:
                return
            units = units["units"]
        db.execute("insert or ignore into deliveries values (?, ?)", (item["id"], units))

    deliver(fresh, False)
    deliver(pending, plan == "drain")
    deliver(pending, plan == "drain")  # retry while the same reader is available
    deliver(pending, False)  # late retry after retirement

    def delivered_once(item):
        return db.execute("select count(*), sum(units) from deliveries where job = ?",
                          (item["id"],)).fetchone() == (1, 7)

    checks = {"fresh_legacy_job": fresh["payload"] == 7 and delivered_once(fresh),
              "inflight_delivered_once": delivered_once(pending)}
    db.close()
    return checks


if __name__ == "__main__":
    request = json.load(sys.stdin)
    if request["identity"] is not None or request["traits"]:
        sys.exit("the example supports anonymous assignments only")
    if sys.argv[1] == "capture":
        if assignment() is not True:
            raise ValueError("capture did not use the new writer")
        result = job(True)
        if not replay("drain", result, job(True))["inflight_delivered_once"]:
            raise ValueError("the pending job could not complete before rollback")
    else:
        result = replay(request["plan"], request["snapshot"], job(assignment()))
    print(json.dumps(result))
