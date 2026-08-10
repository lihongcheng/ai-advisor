"""聊天接口并发压测：模拟多用户同时提问，测全链路上限。"""
import concurrent.futures as cf
import json
import sys
import time
import urllib.request

URL = "http://localhost:8080/api/v1/chat"

# 30 个互不相同的问题，确保语义缓存无法互相命中
QUESTIONS = [
    "高血压人群能吃海带吗", "糖尿病能吃玉米吗", "高血脂能吃鸡蛋吗",
    "老人晚上失眠怎么办", "痛风能吃豆腐吗", "胃病能喝牛奶吗",
    "贫血吃什么补血", "骨质疏松要补什么", "便秘吃什么好",
    "脂肪肝饮食注意什么", "冠心病能吃肥肉吗", "肾病能吃香蕉吗",
    "感冒了吃什么好得快", "关节炎能喝骨头汤吗", "白内障吃胡萝卜有用吗",
    "更年期潮热吃什么", "前列腺肥大饮食禁忌", "慢性支气管炎食疗方",
    "低血压怎么调理", "甲状腺结节能吃碘盐吗", "胆结石能吃油腻吗",
    "胃溃疡能吃辣椒吗", "痔疮出血吃什么", "口腔溃疡缺什么维生素",
    "干眼症吃什么好", "耳鸣吃什么调理", "手脚冰凉怎么食疗",
    "白发变多吃什么", "记忆力下降补什么", "免疫力低吃什么",
]


def one_call(q):
    body = json.dumps({"question": q}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"})
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            resp.read()
            return time.perf_counter() - t0, resp.status
    except Exception as e:
        return time.perf_counter() - t0, str(e)[:60]


def bench(tag, concurrency, questions):
    with cf.ThreadPoolExecutor(max_workers=concurrency) as pool:
        t0 = time.perf_counter()
        results = list(pool.map(one_call, questions))
        wall = time.perf_counter() - t0
    ok = [r[0] for r in results if r[1] == 200]
    fails = [r[1] for r in results if r[1] != 200]
    avg = sum(ok) / len(ok) * 1000 if ok else 0
    slow = max(ok) * 1000 if ok else 0
    print(f"{tag} | 并发 {concurrency:>3} | 成功 {len(ok):>2}/{len(results)} | "
          f"总耗时 {wall:5.1f}s | 折合QPS {len(ok)/wall:5.1f} | 平均延迟 {avg:6.0f}ms | 最慢 {slow:6.0f}ms | 失败 {fails[:2] or '-'}")


if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else "miss"
    if mode == "miss":
        for conc in (5, 10, 20, 30):
            bench("全miss", conc, QUESTIONS[:conc])
            time.sleep(3)
    else:  # hit：同一个已缓存问题，所有人问一样的
        same = ["怎么联系指导师"] * 30
        for conc in (10, 30):
            bench("全hit ", conc, same[:conc])
            time.sleep(2)
