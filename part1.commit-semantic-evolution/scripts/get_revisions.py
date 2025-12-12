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

# Global model cache
BIAS_MODEL_NAME = "newsmediabias/UnBIAS-classifier"
TOPIC_MODEL_NAME = "MoritzLaurer/mDeBERTa-v3-base-mnli-xnli"

tokenizer = None
model = None
topic_pipeline = None
bias_pipeline = None
zero_shot_pipeline = None # New pipeline for topic classification

device = "cuda" if torch.cuda.is_available() else "cpu"
device_id = 0 if torch.cuda.is_available() else -1

print(f"Analysis device: {device}")

def load_models():
    global topic_pipeline, bias_pipeline, zero_shot_pipeline
    if bias_pipeline is None:
        print(f"Loading bias model {BIAS_MODEL_NAME}...")
        try:
            bias_pipeline = pipeline("text-classification", model=BIAS_MODEL_NAME, device=device_id, truncation=True, max_length=512, top_k=None)
        except Exception as e:
            print(f"Failed to load bias model: {e}")

    if zero_shot_pipeline is None: # Use zero-shot for topic classification
        print(f"Loading zero-shot classification model {TOPIC_MODEL_NAME} for topic...")
        try:
            zero_shot_pipeline = pipeline("zero-shot-classification", model=TOPIC_MODEL_NAME, device=device_id)
        except Exception as e:
            print(f"Failed to load zero-shot topic model: {e}")

def get_bias_data(text):
    if not text or not text.strip():
        return 0.0, "Neutral"

    load_models()
    
    score = 0.0
    label = "Neutral"
    
    if bias_pipeline:
        try:
            results = bias_pipeline(text)
            if results and len(results) > 0:
                scores = {item['label']: item['score'] for item in results[0]}
                
                # Use max label
                best_label = max(scores, key=scores.get)
                label = best_label
                
                # Map labels to scores
                # Check actual labels for UnBIAS-classifier.
                # Assuming: "Neutral", "Slightly Biased", "Highly Biased" based on model card.
                
                s_neutral = scores.get("Neutral", scores.get("LABEL_0", 0.0))
                s_slight = scores.get("Slightly Biased", scores.get("LABEL_1", 0.0))
                s_high = scores.get("Highly Biased", scores.get("LABEL_2", 0.0))
                
                # Continuous score: 0.0 (Neutral) -> 1.0 (Highly Biased)
                # Weighted sum?
                score = (s_slight * 0.5) + (s_high * 1.0)
                
        except Exception as e:
            print(f"  Bias prediction error: {e}")
            
    return score, label

def predict_topic(text):
    if not text or not text.strip():
        return "N/A"
        
    load_models()
    
    topic = "N/A"
    if zero_shot_pipeline:
        try:
            candidate_labels = [
                "Politics", "International Relations", "Conflict", "Geography",
                "History", "Culture", "Economy", "Technology", "Social Issues",
                "Biography", "Science", "Arts", "Sports", "Neutral", "Propaganda"
            ]
            
            # Zero-shot classification returns a list of scores for labels
            # We want the top one
            res = zero_shot_pipeline(text, candidate_labels=candidate_labels)
            if res and len(res['labels']) > 0:
                topic = res['labels'][0] # Top scoring label
        except Exception as e:
            print(f"  Topic prediction error: {e}")
            
    return topic

def _is_ip_address(user_string):
    ipv4_pattern = re.compile(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$")
    ipv6_pattern = re.compile(r"^\[?([0-9a-fA-F:]+){1,4}(:[0-9a-fA-F]{1,4}){1,4}\]?$")

    if ipv4_pattern.match(user_string):
        return True
    if ipv6_pattern.match(user_string):
        return True
    return False

def get_structured_changes(prev_content, curr_content, lang='en'):
    try:
        et = StructuredEditTypes("wikitext", prev_content=prev_content, curr_content=curr_content, lang=lang)
        diff = et.get_diff()
    except Exception as e:
        print(f"  Diff calculation error: {e}")
        return []
    
    changes = []
    
    # Process Node Edits (Templates, Links)
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
                changes.append({
                    'type': 'Text:Sentence:Insert',
                    'before': "",
                    'after': text_edit.text,
                    'desc': f"Inserted Sentence"
                })
            elif text_edit.edittype == 'remove':
                changes.append({
                    'type': 'Text:Sentence:Remove',
                    'before': text_edit.text,
                    'after': "",
                    'desc': f"Removed Sentence"
                })
            elif text_edit.edittype == 'change':
                changes.append({
                    'type': 'Text:Sentence:Change',
                    'before': text_edit.text, 
                    'after': text_edit.text, 
                    'desc': f"Changed Sentence"
                })
                
    return changes

DB_PATH = "data/wiki.db"

def get_revisions(debug_mode=None):
    if debug_mode is None:
        parser = argparse.ArgumentParser()
        parser.add_argument("--debug", action="store_true", help="Enable debug output")
        args, _ = parser.parse_known_args()
        debug_mode = args.debug

    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()

    # Get articles from DB
    c.execute("SELECT url, coord FROM articles")
    articles_from_db = c.fetchall()
    
    for article_url, article_coord in articles_from_db:
        if debug_mode: print(f"Fetching revisions for {article_url}...")
        
        # (Rest of the function fetches revisions from Wikipedia API)
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
            url = api_url + "?" + urllib.parse.urlencode(params)
            req = urllib.request.Request(url, headers={"User-Agent": "MyResearchBot/1.0"})
            with urllib.request.urlopen(req) as response:
                data = json.loads(response.read().decode())

            page_id = list(data["query"]["pages"].keys())[0]
            if page_id == "-1":
                print(f"  Article not found: {title}")
                break
            
            revisions = data["query"]["pages"][page_id].get("revisions", [])
            revisions_info.extend(revisions)

            if "continue" in data:
                params.update(data["continue"])
            else:
                break

        print(f"  Processing {len(revisions_info)} revisions...")
        
        for i in range(len(revisions_info)):
            rev = revisions_info[i]
            revid = rev.get("revid")
            
            curr_content = rev.get("slots", {}).get("main", {}).get("*", rev.get("*", ""))
            prev_content = ""
            if i + 1 < len(revisions_info):
                prev_rev = revisions_info[i+1]
                prev_content = prev_rev.get("slots", {}).get("main", {}).get("*", prev_rev.get("*", ""))
            
            if debug_mode: print(f"DEBUG: Processing Rev {revid}...")
            
            changes = get_structured_changes(prev_content, curr_content, lang=lang)
            
            if not changes:
                if debug_mode: print(f"DEBUG: Rev {revid} - No structured changes found.")
                continue
                
            if debug_mode: print(f"DEBUG: Rev {revid} - Found {len(changes)} changes.")
            
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
                
                if not clean_before and not clean_after:
                    continue

                if debug_mode: print(f"DEBUG: Computing bias for Prev (Rev {revisions_info[i+1].get('revid') if i+1 < len(revisions_info) else 'N/A'})")
                score_before, label_before = get_bias_data(clean_before)
                
                if debug_mode: print(f"DEBUG: Computing bias for Curr (Rev {revid})")
                score_after, label_after = get_bias_data(clean_after)
                
                diff_score = score_after - score_before
                
                save_change = False
                
                if abs(diff_score) > 0.1: # Threshold for bias change
                    save_change = True
                    if debug_mode: print(f"DEBUG: Keeping Change - Bias delta {diff_score:.4f} ({label_before} -> {label_after})")
                elif score_after > 0.3: # Keep if somewhat biased
                    save_change = True
                    if debug_mode: print(f"DEBUG: Keeping Change - Biased {score_after:.4f} ({label_after})")
                    
                if save_change:
                    sub_id = f"{revid}-{sub_id_counter:04d}"
                    sub_id_counter += 1
                    
                    user = rev.get("user", "N/A")
                    timestamp = rev.get("timestamp", "N/A")
                    
                    is_ip = _is_ip_address(user)
                    
                    text_for_analysis = after_text if after_text.strip() else before_text
                    ai_topic = predict_topic(text_for_analysis)
                    
                    # Insert into SQLite
                    c.execute('''
                        INSERT OR REPLACE INTO revisions (
                            id, original_revid, article_url, user, timestamp, 
                            diff_before, diff_after, change_type, change_desc,
                            bias_score_before, bias_score_after, bias_delta,
                            bias_label_before, bias_label_after, ai_topic, is_ip
                        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    ''', (
                        sub_id,
                        rev.get("revid"), # original_revid
                        article_url,
                        user,
                        timestamp,
                        before_text,
                        after_text,
                        change['type'],
                        change['desc'],
                        score_before,
                        score_after,
                        diff_score,
                        label_before,
                        label_after,
                        ai_topic,
                        1 if is_ip else 0
                    ))
                    if debug_mode: print(f"    Saved sub-rev {sub_id} | Topic: {ai_topic} | Bias Delta: {diff_score:.4f}")
                    conn.commit() # Commit immediately after saving a revision

    conn.commit()
    conn.close()
    print(f"Finished processing revisions and saving to {DB_PATH}")

if __name__ == "__main__":
    get_revisions()