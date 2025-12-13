import json
import urllib.parse
import urllib.request
import re
import os
import sys
import argparse
from mwedittypes import StructuredEditTypes
from bs4 import BeautifulSoup
import mwparserfromhell
import torch
from transformers import AutoTokenizer, AutoModelForSequenceClassification, pipeline
import numpy as np
import sqlite3
import multiprocessing

# Global model cache (per process)
BIAS_MODEL_NAME = "newsmediabias/UnBIAS-classifier"
TOPIC_MODEL_NAME = "MoritzLaurer/mDeBERTa-v3-base-mnli-xnli"

# These will be initialized per process
bias_pipeline = None
zero_shot_pipeline = None 

DB_PATH = "data/wiki.db"

def load_models(device_id):
    global bias_pipeline, zero_shot_pipeline
    
    device = device_id if device_id >= 0 else -1
    
    if bias_pipeline is None:
        print(f"[GPU {device_id}] Loading bias model {BIAS_MODEL_NAME}...")
        try:
            bias_pipeline = pipeline("text-classification", model=BIAS_MODEL_NAME, device=device, truncation=True, max_length=512, top_k=None)
        except Exception as e:
            print(f"[GPU {device_id}] Failed to load bias model: {e}")

    if zero_shot_pipeline is None: 
        print(f"[GPU {device_id}] Loading zero-shot classification model {TOPIC_MODEL_NAME} for topic...")
        try:
            zero_shot_pipeline = pipeline("zero-shot-classification", model=TOPIC_MODEL_NAME, device=device)
        except Exception as e:
            print(f"[GPU {device_id}] Failed to load zero-shot topic model: {e}")

def get_bias_data(text, device_id):
    if not text or not text.strip():
        return 0.0, "Neutral"

    load_models(device_id)

    score = 0.0
    label = "Neutral"

    if bias_pipeline:
        try:
            results = bias_pipeline(text)
            if results and len(results) > 0:
                scores = {item['label']: item['score'] for item in results[0]}
                best_label = max(scores, key=scores.get)
                label = best_label
                s_slight = scores.get("Slightly Biased", scores.get("LABEL_1", 0.0))
                s_high = scores.get("Highly Biased", scores.get("LABEL_2", 0.0))
                score = (s_slight * 0.5) + (s_high * 1.0)
        except Exception as e:
            print(f"  Bias prediction error: {e}")

    return score, label

def predict_topic(text, device_id):
    if not text or not text.strip():
        return "N/A"

    load_models(device_id)

    topic = "N/A"
    if zero_shot_pipeline:
        try:
            with open("data/topic_categories.json", "r") as f:
                candidate_labels = json.load(f)
            res = zero_shot_pipeline(text, candidate_labels=candidate_labels)
            if res and len(res['labels']) > 0:
                topic = res['labels'][0] 
        except Exception as e:
            print(f"  Topic prediction error: {e}")

    return topic

def predict_political_stance(text, device_id):
    if not text or not text.strip():
        return "N/A"

    load_models(device_id)

    stance = "N/A"
    if zero_shot_pipeline:
        try:
            with open("data/political_categories.json", "r") as f:
                candidate_labels = json.load(f)
            res = zero_shot_pipeline(text, candidate_labels=candidate_labels)
            if res and len(res['labels']) > 0:
                stance = res['labels'][0]
        except Exception as e:
            print(f"  Political stance prediction error: {e}")

    return stance

def _is_ip_address(user_string):
    ipv4_pattern = re.compile(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$")
    ipv6_pattern = re.compile(r"^\[?([0-9a-fA-F:]+){1,4}(:[0-9a-fA-F]{1,4}){1,4}\]?$")
    if ipv4_pattern.match(user_string): return True
    if ipv6_pattern.match(user_string): return True
    return False

def get_structured_changes(prev_content, curr_content, lang='en'):
    try:
        et = StructuredEditTypes("wikitext", prev_content=prev_content, curr_content=curr_content, lang=lang)
        diff = et.get_diff()
    except Exception as e:
        # print(f"  Diff calculation error: {e}")
        return []

    changes = []
    # Process Node Edits
    for node in diff.get('node-edits', []):
        if node.type in ['Template', 'Wikilink', 'Reference']:
             if node.edittype == 'change':
                 for change in node.changes:
                     field = change[0]
                     before_val = ""
                     after_val = ""
                     if field == 'parameter':
                         if change[1]: before_val = change[1][1]
                         if change[2]: after_val = change[2][1]
                     elif field == 'title':
                         if change[1]: before_val = change[1]
                         if change[2]: after_val = change[2]
                     else:
                         if len(change) > 1 and change[1]: before_val = str(change[1])
                         if len(change) > 2 and change[2]: after_val = str(change[2])
                     if before_val == after_val: continue
                     changes.append({
                         'type': f"Node:{node.type}:{field}",
                         'before': before_val,
                         'after': after_val,
                         'desc': f"{node.type} {node.name} changed {field}"
                     })

    # Process Text Edits
    for text_edit in diff.get('text-edits', []):
        if text_edit.type == 'Sentence':
            if text_edit.edittype == 'insert':
                changes.append({'type': 'Text:Sentence:Insert', 'before': "", 'after': text_edit.text, 'desc': f"Inserted Sentence"})
            elif text_edit.edittype == 'remove':
                changes.append({'type': 'Text:Sentence:Remove', 'before': text_edit.text, 'after': "", 'desc': f"Removed Sentence"})
            elif text_edit.edittype == 'change':
                changes.append({'type': 'Text:Sentence:Change', 'before': text_edit.text, 'after': text_edit.text, 'desc': f"Changed Sentence"})
    return changes

def process_batch(articles, device_id, debug_mode):
    """
    Worker function to process a batch of articles on a specific device.
    """
    conn = sqlite3.connect(DB_PATH, timeout=60.0)
    c = conn.cursor()
    
    print(f"[Worker GPU:{device_id}] Starting processing of {len(articles)} articles.")
    
    for article_url, article_coord in articles:
        if debug_mode: print(f"[GPU:{device_id}] Fetching revisions for {article_url}...")

        title = urllib.parse.unquote(article_url.split("/")[-1])
        parsed_url = urllib.parse.urlparse(article_url)
        domain = parsed_url.netloc
        lang = domain.split('.')[0]
        api_url = f"{parsed_url.scheme}://{domain}/w/api.php"

        revisions_info = []
        params = {
            "action": "query",
            "prop": "revisions",
            "titles": title,
            "rvprop": "timestamp|user|ids|content",
            "rvslots": "main",
            "rvlimit": "max",
            "format": "json",
        }

        while True:
            url_req = api_url + "?" + urllib.parse.urlencode(params)
            req = urllib.request.Request(url_req, headers={"User-Agent": "MyResearchBot/1.0"})
            try:
                with urllib.request.urlopen(req) as response:
                    data = json.loads(response.read().decode())
            except Exception as e:
                print(f"Error fetching {url_req}: {e}")
                break

            page_ids = list(data["query"]["pages"].keys())
            if not page_ids or page_ids[0] == "-1":
                print(f"  Article not found: {title}")
                break
            
            page_id = page_ids[0]
            revisions = data["query"]["pages"][page_id].get("revisions", [])
            revisions_info.extend(revisions)

            if "continue" in data:
                params.update(data["continue"])
            else:
                break
        
        if debug_mode: print(f"[GPU:{device_id}] Processing {len(revisions_info)} revisions for {title}...")

        for i in range(len(revisions_info)):
            rev = revisions_info[i]
            revid = rev.get("revid")

            curr_content = rev.get("slots", {}).get("main", {}).get("*", rev.get("*", ""))
            prev_content = ""
            if i + 1 < len(revisions_info):
                prev_rev = revisions_info[i+1]
                prev_content = prev_rev.get("slots", {}).get("main", {}).get("*", prev_rev.get("*", ""))

            changes = get_structured_changes(prev_content, curr_content, lang=lang)

            if not changes: continue

            sub_id_counter = 1
            for change in changes:
                before_text = change['before']
                after_text = change['after']

                try:
                    clean_before = mwparserfromhell.parse(before_text).strip_code().strip()
                    clean_after = mwparserfromhell.parse(after_text).strip_code().strip()
                except Exception:
                    clean_before = before_text.strip()
                    clean_after = after_text.strip()

                if not clean_before and not clean_after: continue

                score_before, label_before = get_bias_data(clean_before, device_id)
                score_after, label_after = get_bias_data(clean_after, device_id)

                diff_score = score_after - score_before
                save_change = False

                if abs(diff_score) > 0.1: save_change = True
                elif score_after > 0.3: save_change = True

                if save_change:
                    sub_id = f"{revid}-{sub_id_counter:04d}"
                    sub_id_counter += 1

                    user = rev.get("user", "N/A")
                    timestamp = rev.get("timestamp", "N/A")
                    is_ip = _is_ip_address(user)

                    text_for_analysis = after_text if after_text.strip() else before_text
                    ai_context = text_for_analysis
                    
                    if after_text.strip() and curr_content:
                        try:
                            matches = [m.start() for m in re.finditer(re.escape(after_text), curr_content)]
                        except Exception:
                            matches = []

                        if matches:
                            best_idx = matches[0]
                            if len(matches) > 1 and before_text.strip() and prev_content:
                                try:
                                    prev_matches = [m.start() for m in re.finditer(re.escape(before_text), prev_content)]
                                    if prev_matches:
                                        hint_idx = prev_matches[0]
                                        best_idx = min(matches, key=lambda x: abs(x - hint_idx))
                                except Exception:
                                    pass
                            
                            start = max(0, best_idx - int(len(after_text) * 0.5))
                            end = min(len(curr_content), best_idx + len(after_text) + int(len(after_text) * 0.5))
                            ai_context = curr_content[start:end]

                    ai_topic = predict_topic(ai_context, device_id)
                    ai_political_stance = predict_political_stance(ai_context, device_id)

                    try:
                        c.execute('''
                            INSERT OR REPLACE INTO revisions (
                                id, original_revid, article_url, user, timestamp,
                                diff_before, diff_after, change_type, change_desc,
                                bias_score_before, bias_score_after, bias_delta,
                                bias_label_before, bias_label_after, ai_topic, ai_political_stance, is_ip, content
                            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                        ''', (
                            sub_id, rev.get("revid"), article_url, user, timestamp,
                            before_text, after_text, change['type'], change['desc'],
                            score_before, score_after, diff_score, label_before, label_after,
                            ai_topic, ai_political_stance, 1 if is_ip else 0, ai_context
                        ))
                        conn.commit()
                        if debug_mode: print(f"[GPU:{device_id}] Saved {sub_id}")
                    except sqlite3.OperationalError as e:
                        print(f"DB Error on {sub_id}: {e}")

    conn.close()
    print(f"[Worker GPU:{device_id}] Finished.")


def get_revisions(debug_mode=None, limit=None):
    if debug_mode is None:
        parser = argparse.ArgumentParser()
        parser.add_argument("--debug", action="store_true", help="Enable debug output")
        parser.add_argument("--limit", type=int, help="Limit number of articles to process")
        args, _ = parser.parse_known_args()
        debug_mode = args.debug
        if limit is None: limit = args.limit

    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute("SELECT url, coord FROM articles")
    articles_from_db = c.fetchall()
    conn.close()

    if limit and limit > 0:
        articles_from_db = articles_from_db[:limit]
        print(f"Limiting to {limit} articles.")

    if not articles_from_db:
        print("No articles to process.")
        return

    num_gpus = torch.cuda.device_count()
    
    if num_gpus > 1:
        print(f"Found {num_gpus} GPUs. Distributing work...")
        
        # Split articles
        chunks = np.array_split(articles_from_db, num_gpus)
        processes = []
        
        # Use Spawn context for compatibility
        ctx = multiprocessing.get_context('spawn')
        
        for i in range(num_gpus):
            chunk = chunks[i].tolist() # Convert numpy array back to list
            if not chunk: continue
            
            p = ctx.Process(target=process_batch, args=(chunk, i, debug_mode))
            p.start()
            processes.append(p)
        
        for p in processes:
            p.join()
            
    else:
        # Single GPU or CPU
        device_id = 0 if torch.cuda.is_available() else -1
        print(f"Using single device: {device_id}")
        process_batch(articles_from_db, device_id, debug_mode)

    print(f"Finished processing revisions.")

if __name__ == "__main__":
    get_revisions()
