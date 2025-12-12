import sqlite3
import os

DB_PATH = "data/wiki.db"

def init_db():
    # Ensure the data directory exists
    os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)
    
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    
    # Create articles table
    c.execute('''
        CREATE TABLE IF NOT EXISTS articles (
            url TEXT PRIMARY KEY,
            coord TEXT
        )
    ''')
    
    # Create revisions table
    # Storing all analysis data + manual labels
    c.execute('''
        CREATE TABLE IF NOT EXISTS revisions (
            id TEXT PRIMARY KEY,
            original_revid INTEGER,
            article_url TEXT,
            user TEXT,
            timestamp TEXT,
            diff_before TEXT,
            diff_after TEXT,
            change_type TEXT,
            change_desc TEXT,
            bias_score_before REAL,
            bias_score_after REAL,
            bias_delta REAL,
            bias_label_before TEXT,
            bias_label_after TEXT,
            ai_topic TEXT,
            is_ip INTEGER,
            manual_bias TEXT,
            manual_topic TEXT,
            FOREIGN KEY (article_url) REFERENCES articles (url)
        )
    ''')
    
    conn.commit()
    conn.close()
    print(f"Database initialized at {DB_PATH}")

if __name__ == "__main__":
    init_db()
