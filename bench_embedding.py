"""Embedding API QPS 压测：阶梯并发打 SiliconFlow bge-m3，测吞吐量与限流点。"""
import concurrent.futures as cf
import json
import os
import statistics
import time
import urllib.request

BASE_URL = os.environ["EMBEDDING_BASE_URL"]
API_KEY = os.environ["EMBEDDING_API_KEY"]
MODEL = os.environ["EMBEDDING_MODEL"]
TEXT = "高血压人群每日食盐摄入量建议不超过5克"  # 模拟真实问答长度


def one_call(_):
    body = json.dumps({"model": MODEL, "input": [TEXT]}).encode()
    req = urllib.request.Request(
        BASE_URL + "/embeddings", data=body,
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + API_KEY})
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            resp.read()
            return time.perf_counter() - t0, resp.status
    except urllib.error.HTTPError as e:
        return time.perf_counter() - t0, e.code
    except Exception:
        return time.perf_counter() - t0, -1


def bench(concurrency, total):
    with cf.ThreadPoolExecutor(max_workers=concurrency) as pool:
        t0 = time.perf_counter()
        results = list(pool.map(one_call, range(total)))
        wall = time.perf_counter() - t0
    lat = [r[0] for r in results if r[1] == 200]
    errors = {}
    for _, code in results:
        if code != 200:
            errors[code] = errors.get(code, 0) + 1
    qps = len(results) / wall
    p50 = statistics.median(lat) * 1000 if lat else 0
    p95 = sorted(lat)[int(len(lat) * 0.95)] * 1000 if lat else 0
    print(f"并发 {concurrency:>3} | 请求 {total:>3} | 实测QPS {qps:7.1f} | "
          f"p50 {p50:6.0f}ms | p95 {p95:6.0f}ms | 成功 {len(lat):>3} | 失败 {errors or '-'}")
    return qps, errors


if __name__ == "__main__":
    print(f"目标: {BASE_URL} 模型: {MODEL}\n")
    # 阶梯加压：每档请求数 = 并发 × 10，保证足够样本；总量 ~500 次，成本可忽略
    for conc, total in [(1, 10), (5, 50), (10, 100), (20, 200), (40, 400)]:
        bench(conc, total)
        time.sleep(2)  # 档间休息，避免跨档触发限流干扰
