import sqlite3
import json
import os
from collections import defaultdict

DB_PATH = "data/wiki.db"

def print_confusion_matrix(title, matrix, labels):
    # Calculate column widths
    label_width = max(len(l) for l in labels) + 2
    col_width = max(len(l) for l in labels) + 2
    
    # Header
    print(f"\n--- {title} Confusion Matrix (Rows: Human, Cols: AI) ---")
    header = " " * label_width + "|"
    for l in labels:
        header += f" {l:^{col_width-2}} |"
    print(header)
    print("-" * len(header))

    # Rows
    for row_label in labels:
        row_str = f"{row_label:<{label_width}} |"
        for col_label in labels:
            val = matrix[row_label][col_label]
            if val == 0:
                val_str = "."
            else:
                val_str = str(val)
            row_str += f" {val_str:^{col_width-2}} |"
        print(row_str)

def print_distribution_comparison(title, human_counts, ai_counts, labels):
    print(f"\n--- {title} Distribution Bias (Systematic Error) ---")
    print(f"{'Category':<20} | {'Human Count':<12} | {'AI Count':<12} | {'Diff (AI-Human)':<15}")
    print("-" * 65)
    
    # Sort by magnitude of difference
    sorted_labels = sorted(labels, key=lambda l: abs(ai_counts[l] - human_counts[l]), reverse=True)
    
    for l in sorted_labels:
        h = human_counts.get(l, 0)
        a = ai_counts.get(l, 0)
        diff = a - h
        print(f"{l:<20} | {h:<12} | {a:<12} | {diff:<+15}")

def print_classification_report(title, matrix, labels, human_counts):
    print(f"\n--- {title} Classification Report ---")
    print(f"{'Class':<20} | {'Precision':<10} | {'Recall':<10} | {'F1-Score':<10} | {'Support':<10}")
    print("-" * 75)

    precisions = []
    recalls = []
    f1s = []
    supports = []
    
    total_correct = 0
    total_samples = 0

    # Calculate per-class metrics
    for label in labels:
        tp = matrix[label][label]
        
        # False Positives: Sum of column 'label' - TP
        fp = sum(matrix[row][label] for row in labels) - tp
        
        # False Negatives: Sum of row 'label' - TP
        fn = sum(matrix[label][col] for col in labels) - tp
        
        support = human_counts.get(label, 0)
        total_samples += support
        total_correct += tp

        precision = tp / (tp + fp) if (tp + fp) > 0 else 0.0
        recall = tp / (tp + fn) if (tp + fn) > 0 else 0.0
        f1 = 2 * (precision * recall) / (precision + recall) if (precision + recall) > 0 else 0.0

        precisions.append(precision)
        recalls.append(recall)
        f1s.append(f1)
        supports.append(support)

        print(f"{label:<20} | {precision:<10.4f} | {recall:<10.4f} | {f1:<10.4f} | {support:<10}")

    print("-" * 75)
    
    # Averages
    if len(labels) > 0:
        macro_prec = sum(precisions) / len(labels)
        macro_rec = sum(recalls) / len(labels)
        macro_f1 = sum(f1s) / len(labels)
        
        accuracy = total_correct / total_samples if total_samples > 0 else 0.0
        
        # Weighted F1
        weighted_f1 = sum(f * s for f, s in zip(f1s, supports)) / total_samples if total_samples > 0 else 0.0

        print(f"{'Accuracy':<20} | {'':<10} | {'':<10} | {accuracy:<10.4f} | {total_samples:<10}")
        print(f"{'Macro Avg':<20} | {macro_prec:<10.4f} | {macro_rec:<10.4f} | {macro_f1:<10.4f} | {total_samples:<10}")
        print(f"{'Weighted Avg':<20} | {'':<10} | {'':<10} | {weighted_f1:<10.4f} | {total_samples:<10}")

def print_guide():
    print("\n" + "="*75)
    print("GUIDE: How to Interpret These Results")
    print("="*75)
    print("1. Confusion Matrix (Rows = Human, Cols = AI)")
    print("   - Shows the raw count of predictions vs ground truth.")
    print("   - Diagonal numbers (top-left to bottom-right) are CORRECT predictions.")
    print("   - Off-diagonal numbers are ERRORS (e.g., Row 'A', Col 'B' means Human said 'A' but AI predicted 'B').")
    print("\n2. Distribution Bias (Systematic Error)")
    print("   - Compares total counts for each category.")
    print("   - Positive Diff (+): The AI predicts this label MORE often than humans (Over-prediction).")
    print("   - Negative Diff (-): The AI predicts this label LESS often than humans (Under-prediction).")
    print("\n3. Classification Report Metrics")
    print("   - Precision: Accuracy of positive predictions. (TP / (TP + FP))")
    print("     \"When the AI predicts 'X', how often is it right?\"")
    print("   - Recall: Sensitivity or Hit Rate. (TP / (TP + FN))")
    print("     \"Out of all actual 'X' cases, how many did the AI find?\"")
    print("   - F1-Score: Harmonic mean of Precision and Recall.")
    print("     Balanced metric. Useful if you have uneven class distribution.")
    print("   - Support: The number of human-labeled samples for that category.")
    print("="*75 + "\n")

def main():
    if not os.path.exists(DB_PATH):
        print(f"Error: Database not found at {DB_PATH}")
        return

    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()

    # Updated query to include bias_score_after
    query = """
        SELECT 
            r.article_url,
            a.coord,
            r.ai_topic,
            r.manual_topic,
            r.ai_political_stance,
            r.manual_bias,
            r.bias_score_after
        FROM revisions r
        LEFT JOIN articles a ON r.article_url = a.url
        WHERE r.manual_topic IS NOT NULL OR r.manual_bias IS NOT NULL
    """
    
    try:
        c.execute(query)
        rows = c.fetchall()
    except sqlite3.OperationalError as e:
        print(f"Database error: {e}")
        conn.close()
        return
    
    if not rows:
        print("No manually labeled data found in the database.")
        conn.close()
        return

    # Metrics
    topic_matrix = defaultdict(lambda: defaultdict(int))
    stance_matrix = defaultdict(lambda: defaultdict(int))
    
    topic_human_counts = defaultdict(int)
    topic_ai_counts = defaultdict(int)
    
    stance_human_counts = defaultdict(int)
    stance_ai_counts = defaultdict(int)

    all_topics = set()
    all_stances = set()

    # Per article aggregation
    # We use inner defaultdicts to count frequencies of labels per article
    article_stats = defaultdict(lambda: {
        'coord': None,
        'topic_matches': 0, 'topic_total': 0,
        'stance_matches': 0, 'stance_total': 0,
        'bias_sum': 0.0, 'bias_count': 0,
        'human_topics': defaultdict(int),
        'ai_topics': defaultdict(int),
        'human_stances': defaultdict(int),
        'ai_stances': defaultdict(int)
    })

    for row in rows:
        url, coord, ai_topic, man_topic, ai_stance, man_bias, bias_score = row
        
        # Track coordinates
        if coord:
            article_stats[url]['coord'] = coord

        # Track Bias Score
        if bias_score is not None:
            article_stats[url]['bias_sum'] += bias_score
            article_stats[url]['bias_count'] += 1

        # Topic Analysis
        if man_topic and ai_topic:
            t_ai = ai_topic.strip()
            t_man = man_topic.strip()
            
            all_topics.add(t_ai)
            all_topics.add(t_man)
            
            topic_matrix[t_man][t_ai] += 1
            topic_human_counts[t_man] += 1
            topic_ai_counts[t_ai] += 1
            
            article_stats[url]['topic_total'] += 1
            article_stats[url]['human_topics'][t_man] += 1
            article_stats[url]['ai_topics'][t_ai] += 1
            
            if t_ai == t_man:
                article_stats[url]['topic_matches'] += 1

        # Stance Analysis
        if man_bias and ai_stance:
            s_ai = ai_stance.strip()
            s_man = man_bias.strip()
            
            all_stances.add(s_ai)
            all_stances.add(s_man)
            
            stance_matrix[s_man][s_ai] += 1
            stance_human_counts[s_man] += 1
            stance_ai_counts[s_ai] += 1

            article_stats[url]['stance_total'] += 1
            article_stats[url]['human_stances'][s_man] += 1
            article_stats[url]['ai_stances'][s_ai] += 1

            if s_ai == s_man:
                article_stats[url]['stance_matches'] += 1
        

    # Output detailed report
    
    # 1. Political Stance Report
    if all_stances:
        sorted_stances = sorted(list(all_stances))
        print_confusion_matrix("Political Stance", stance_matrix, sorted_stances)
        print_distribution_comparison("Political Stance", stance_human_counts, stance_ai_counts, sorted_stances)
        print_classification_report("Political Stance", stance_matrix, sorted_stances, stance_human_counts)
    else:
        print("\nNo Political Stance data to analyze.")

    # 2. Topic Report
    if all_topics:
        sorted_topics = sorted(list(all_topics))
        print_confusion_matrix("Topic", topic_matrix, sorted_topics)
        print_distribution_comparison("Topic", topic_human_counts, topic_ai_counts, sorted_topics)
        print_classification_report("Topic", topic_matrix, sorted_topics, topic_human_counts)
    else:
        print("\nNo Topic data to analyze.")

    # Generate GeoJSON
    features = []
    
    for url, stats in article_stats.items():
        if not stats['coord']:
            continue
            
        try:
            wkt = stats['coord'].upper()
            if wkt.startswith("POINT"):
                content = wkt.replace("POINT", "").replace("(", "").replace(")", "").strip()
                parts = content.split()
                if len(parts) >= 2:
                    lon, lat = float(parts[0]), float(parts[1])
                else:
                    continue
            else:
                continue
        except (ValueError, AttributeError):
            continue
            
        # Calculate stats
        art_t_acc = (stats['topic_matches'] / stats['topic_total']) if stats['topic_total'] > 0 else 0.0
        art_s_acc = (stats['stance_matches'] / stats['stance_total']) if stats['stance_total'] > 0 else 0.0
        avg_bias = (stats['bias_sum'] / stats['bias_count']) if stats['bias_count'] > 0 else 0.0
        
        # Helper to get dominant key
        def get_dominant(d):
            if not d: return "N/A"
            return max(d, key=d.get)

        dom_h_topic = get_dominant(stats['human_topics'])
        dom_ai_topic = get_dominant(stats['ai_topics'])
        dom_h_stance = get_dominant(stats['human_stances'])
        dom_ai_stance = get_dominant(stats['ai_stances'])
        
        feature = {
            "type": "Feature",
            "geometry": {
                "type": "Point",
                "coordinates": [lon, lat]
            },
            "properties": {
                "article": url,
                "topic_agreement": round(art_t_acc, 4),
                "topic_samples": stats['topic_total'],
                "stance_agreement": round(art_s_acc, 4),
                "stance_samples": stats['stance_total'],
                "dominant_human_topic": dom_h_topic,
                "dominant_ai_topic": dom_ai_topic,
                "dominant_human_stance": dom_h_stance,
                "dominant_ai_stance": dom_ai_stance,
                "avg_bias_score": round(avg_bias, 4)
            }
        }
        features.append(feature)
        
    geojson = {
        "type": "FeatureCollection",
        "features": features
    }
    
    output_path = "agreement_map.geojson"
    try:
        with open(output_path, "w") as f:
            json.dump(geojson, f, indent=2)
        print(f"\nGeoJSON saved to '{output_path}' with {len(features)} features.")
    except Exception as e:
        print(f"Error saving GeoJSON: {e}")
        
    print_guide()
    
    # Process Unlabeled Data
    process_unlabeled_data(conn)
    
    # Analyze Trends
    analyze_trends(conn)
    
    conn.close()

def analyze_trends(conn):
    print("\n" + "="*75)
    print("PART 3: Temporal Outliers (Activity & Bias)")
    print("="*75)
    
    c = conn.cursor()
    # Fetch timestamps and bias scores
    query = "SELECT timestamp, bias_score_after FROM revisions ORDER BY timestamp"
    try:
        c.execute(query)
        rows = c.fetchall()
    except sqlite3.OperationalError:
        return

    if not rows:
        print("No trend data available.")
        return

    # Aggregate by Month
    monthly_activity = defaultdict(int)
    monthly_bias_sum = defaultdict(float)
    monthly_bias_count = defaultdict(int)
    
    for ts, bias in rows:
        # timestamp format ex: 2023-10-27T10:00:00Z
        if not ts or len(ts) < 7:
            continue
        month = ts[:7]
        
        monthly_activity[month] += 1
        
        if bias is not None:
            monthly_bias_sum[month] += bias
            monthly_bias_count[month] += 1

    months = sorted(monthly_activity.keys())
    if not months:
        return

    # Helper for stats
    def get_stats(values):
        if not values: return 0.0, 0.0
        mean = sum(values) / len(values)
        variance = sum((x - mean) ** 2 for x in values) / len(values)
        return mean, variance ** 0.5

    # 1. Activity Stats
    act_values = [monthly_activity[m] for m in months]
    mean_act, std_act = get_stats(act_values)

    # 2. Bias Stats
    months_with_bias = [m for m in months if monthly_bias_count[m] > 0]
    bias_values = [monthly_bias_sum[m] / monthly_bias_count[m] for m in months_with_bias]
    mean_bias, std_bias = get_stats(bias_values)

    outliers = []
    threshold = 1.5 # StdDev threshold

    # Check Activity
    for m in months:
        val = monthly_activity[m]
        if std_act > 0:
            z = (val - mean_act) / std_act
            if z > threshold:
                outliers.append((m, "High Activity", f"{val} revs", f"(Mean: {mean_act:.1f})"))
            elif z < -threshold:
                outliers.append((m, "Low Activity", f"{val} revs", f"(Mean: {mean_act:.1f})"))

    # Check Bias
    for m in months_with_bias:
        val = monthly_bias_sum[m] / monthly_bias_count[m]
        if std_bias > 0:
            z = (val - mean_bias) / std_bias
            if z > threshold:
                outliers.append((m, "High Bias Score", f"{val:.4f}", f"(Mean: {mean_bias:.4f})"))
            elif z < -threshold:
                outliers.append((m, "Low Bias Score", f"{val:.4f}", f"(Mean: {mean_bias:.4f})"))

    outliers.sort(key=lambda x: x[0])

    if not outliers:
        print(f"No significant outliers detected (Threshold: > {threshold} StdDev).")
    else:
        print(f"{'Month':<10} | {'Reason':<20} | {'Value':<15} | {'Context':<20}")
        print("-" * 70)
        for row in outliers:
            print(f"{row[0]:<10} | {row[1]:<20} | {row[2]:<15} | {row[3]:<20}")
    
    print("\n" + "="*75)

def process_unlabeled_data(conn):
    print("\n" + "="*75)
    print("PART 2: AI Survey on Unlabeled Data")
    print("="*75)
    
    c = conn.cursor()
    query = """
        SELECT 
            r.article_url,
            a.coord,
            r.ai_topic,
            r.ai_political_stance,
            r.bias_score_after
        FROM revisions r
        LEFT JOIN articles a ON r.article_url = a.url
        WHERE r.manual_topic IS NULL AND r.manual_bias IS NULL
    """
    
    try:
        c.execute(query)
        rows = c.fetchall()
    except sqlite3.OperationalError as e:
        print(f"Database error: {e}")
        return

    if not rows:
        print("No unlabeled data found.")
        return

    # Aggregations
    topic_counts = defaultdict(int)
    stance_counts = defaultdict(int)
    total_bias = 0.0
    bias_count = 0
    
    article_stats = defaultdict(lambda: {
        'coord': None,
        'topics': defaultdict(int),
        'stances': defaultdict(int),
        'bias_sum': 0.0,
        'bias_count': 0,
        'rev_count': 0
    })

    for row in rows:
        url, coord, topic, stance, bias = row
        
        if topic:
            topic_counts[topic] += 1
            article_stats[url]['topics'][topic] += 1
            
        if stance:
            stance_counts[stance] += 1
            article_stats[url]['stances'][stance] += 1
            
        if bias is not None:
            total_bias += bias
            bias_count += 1
            article_stats[url]['bias_sum'] += bias
            article_stats[url]['bias_count'] += 1
            
        if coord:
            article_stats[url]['coord'] = coord
            
        article_stats[url]['rev_count'] += 1

    # Print Summary
    print(f"Total Unlabeled Revisions: {len(rows)}")
    if bias_count > 0:
        print(f"Average Global Bias Score: {total_bias / bias_count:.4f}")
    
    print("\n--- Top AI Topics (Unlabeled) ---")
    for t, count in sorted(topic_counts.items(), key=lambda x: x[1], reverse=True)[:10]:
        print(f"{t:<30} | {count}")
        
    print("\n--- AI Political Stance Distribution (Unlabeled) ---")
    for s, count in sorted(stance_counts.items(), key=lambda x: x[1], reverse=True):
        print(f"{s:<30} | {count}")

    # Generate GeoJSON
    features = []
    
    for url, stats in article_stats.items():
        if not stats['coord']:
            continue
            
        try:
            wkt = stats['coord'].upper()
            if wkt.startswith("POINT"):
                content = wkt.replace("POINT", "").replace("(", "").replace(")", "").strip()
                parts = content.split()
                if len(parts) >= 2:
                    lon, lat = float(parts[0]), float(parts[1])
                else:
                    continue
            else:
                continue
        except (ValueError, AttributeError):
            continue
            
        def get_dominant(d):
            if not d: return "N/A"
            return max(d, key=d.get)
            
        avg_bias = (stats['bias_sum'] / stats['bias_count']) if stats['bias_count'] > 0 else 0.0
        
        feature = {
            "type": "Feature",
            "geometry": {
                "type": "Point",
                "coordinates": [lon, lat]
            },
            "properties": {
                "article": url,
                "revisions_analyzed": stats['rev_count'],
                "dominant_ai_topic": get_dominant(stats['topics']),
                "dominant_ai_stance": get_dominant(stats['stances']),
                "avg_ai_bias": round(avg_bias, 4)
            }
        }
        features.append(feature)

    geojson = {
        "type": "FeatureCollection",
        "features": features
    }
    
    output_path = "ai_survey_map.geojson"
    try:
        with open(output_path, "w") as f:
            json.dump(geojson, f, indent=2)
        print(f"\nAI Survey GeoJSON saved to '{output_path}' with {len(features)} features.")
    except Exception as e:
        print(f"Error saving AI Survey GeoJSON: {e}")

if __name__ == "__main__":
    main()

