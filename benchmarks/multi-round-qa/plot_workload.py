import pandas as pd
import json
import sys 
import matplotlib.pyplot as plt

if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("Usage: python3 script.py <config_file> <key_word>")
        sys.exit(1)
    
    config_file = sys.argv[1]
    keyword = sys.argv[2]
    with open(config_file, 'r') as file:
        for line in file:
            config = json.loads(line.strip())
            num_users = config.get('NUM_USERS', 320)
            num_rounds = config.get('NUM_ROUNDS', 10)
            system_prompt = config.get('SYSTEM_PROMPT', 1000)
            chat_history = config.get('CHAT_HISTORY', 20000)
            answer_len = config.get('ANSWER_LEN', 100)
            ttfts = []
            QPS_RANGE = [0.1, 0.5, 0.9, 1.3, 1.7, 2.1]
            for qps in QPS_RANGE:
                csv_file_name = f"results/stack_qps_{qps}_users_{num_users}_rounds_{num_rounds}_prompt_{system_prompt}_history_{chat_history}_answer_{answer_len}.csv"
                # open file 
                csv_file = pd.read_csv(csv_file_name)
                ttft = csv_file['ttft'].mean()

                ttfts.append(ttft)
            fig, ax = plt.subplots()
            ax.plot(QPS_RANGE, ttfts, marker='o', label=f'Stack')
            ax.set_xlabel('QPS')
            ax.set_ylabel('TTFT (seconds)')

            ttfts = []
            QPS_RANGE = [0.1, 0.5, 0.9, 1.3, 1.7, 2.1]
            for qps in QPS_RANGE:
                csv_file_name = f"results/naive_qps_{qps}_users_{num_users}_rounds_{num_rounds}_prompt_{system_prompt}_history_{chat_history}_answer_{answer_len}.csv"
                # open file 
                csv_file = pd.read_csv(csv_file_name)
                ttft = csv_file['ttft'].mean()

                ttfts.append(ttft)
            ax.plot(QPS_RANGE, ttfts, marker='x', label=f'Basic')
            ax.legend()
            fig.savefig(f"qps_{num_users}_rounds_{num_rounds}_prompt_{system_prompt}_history_{chat_history}_answer_{answer_len}.png")
            